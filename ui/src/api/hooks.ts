import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { fetchJSON } from '../lib/client';
import { usePollingInterval } from '../context/PollingContext';
import type { DashboardResponse } from '../test/mocks/fixtures/dashboard';
import type { HealthResponse } from '../test/mocks/fixtures/health';
import type { RouteResponse } from '../test/mocks/fixtures/routes';

// Read hooks

export function useDashboard() {
  const refetchInterval = usePollingInterval(5000);
  return useQuery<DashboardResponse>({
    queryKey: ['dashboard'],
    queryFn: () => fetchJSON('/dashboard'),
    refetchInterval,
  });
}

export function useHealth() {
  const refetchInterval = usePollingInterval(5000);
  return useQuery<HealthResponse>({
    queryKey: ['health'],
    queryFn: () => fetchJSON('/health'),
    refetchInterval,
  });
}

export function useRoutes() {
  const refetchInterval = usePollingInterval(10000);
  return useQuery<RouteResponse[]>({
    queryKey: ['routes'],
    queryFn: () => fetchJSON('/routes'),
    refetchInterval,
  });
}

export function useBackends() {
  return useQuery({
    queryKey: ['backends'],
    queryFn: () => fetchJSON('/backends'),
  });
}

export function useListeners() {
  return useQuery({
    queryKey: ['listeners'],
    queryFn: () => fetchJSON('/listeners'),
  });
}

export function useCertificates() {
  return useQuery({
    queryKey: ['certificates'],
    queryFn: () => fetchJSON('/certificates'),
  });
}

export function useUpstreams() {
  return useQuery({
    queryKey: ['upstreams'],
    queryFn: () => fetchJSON('/upstreams'),
  });
}

export function useReloadStatus() {
  return useQuery({
    queryKey: ['reload-status'],
    queryFn: () => fetchJSON('/reload/status'),
  });
}

export function useRateLimits() {
  return useQuery({
    queryKey: ['rate-limits'],
    queryFn: () => fetchJSON('/rate-limits'),
  });
}

export function useTrafficShaping() {
  return useQuery({
    queryKey: ['traffic-shaping'],
    queryFn: () => fetchJSON('/traffic-shaping'),
  });
}

export function useRules() {
  return useQuery({
    queryKey: ['rules'],
    queryFn: () => fetchJSON('/rules'),
  });
}

export function useStats() {
  return useQuery({
    queryKey: ['stats'],
    queryFn: () => fetchJSON('/stats'),
  });
}

// Write hooks

export function useDrain() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => fetchJSON('/drain', { method: 'POST' }),
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
      queryClient.invalidateQueries({ queryKey: ['health'] });
    },
  });
}

export function useCachePurge() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => fetchJSON('/cache/purge', { method: 'POST' }),
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
    },
  });
}

export function useCircuitBreakerAction() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ route, action }: { route: string; action: string }) =>
      fetchJSON(`/circuit-breakers/${route}/${action}`, { method: 'POST' }),
    onMutate: async ({ route, action }) => {
      await queryClient.cancelQueries({ queryKey: ['dashboard'] });
      const previous = queryClient.getQueryData<DashboardResponse>(['dashboard']);
      if (previous) {
        const newState = action === 'force-open' ? 'open' : 'closed';
        queryClient.setQueryData<DashboardResponse>(['dashboard'], {
          ...previous,
          circuit_breakers: {
            ...previous.circuit_breakers,
            [route]: {
              ...previous.circuit_breakers[route],
              state: newState,
            },
          },
        });
      }
      return { previous };
    },
    onError: (_err, _vars, context) => {
      if (context?.previous) {
        queryClient.setQueryData(['dashboard'], context.previous);
      }
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
    },
  });
}

export function useReload() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => fetchJSON('/reload', { method: 'POST' }),
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['reload-status'] });
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
      queryClient.invalidateQueries({ queryKey: ['routes'] });
    },
  });
}
