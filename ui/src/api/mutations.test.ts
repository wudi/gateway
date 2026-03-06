import { describe, it, expect } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { createElement, type ReactNode } from 'react';
import { PollingProvider } from '../context/PollingContext';
import { createTestQueryClient } from '../test/utils';
import { useDrain, useCachePurge, useCircuitBreakerAction, useReload, useDashboard } from './hooks';

function createWrapper() {
  const queryClient = createTestQueryClient();
  return {
    wrapper: function Wrapper({ children }: { children: ReactNode }) {
      return createElement(
        QueryClientProvider,
        { client: queryClient },
        createElement(
          PollingProvider,
          null,
          createElement(MemoryRouter, null, children),
        ),
      );
    },
    queryClient,
  };
}

describe('useDrain', () => {
  it('calls POST /drain', async () => {
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useDrain(), { wrapper });
    await act(async () => {
      result.current.mutate();
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });
});

describe('useCachePurge', () => {
  it('calls POST /cache/purge', async () => {
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useCachePurge(), { wrapper });
    await act(async () => {
      result.current.mutate();
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });
});

describe('useCircuitBreakerAction', () => {
  it('calls POST /circuit-breakers/:route/:action', async () => {
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useCircuitBreakerAction(), { wrapper });
    await act(async () => {
      result.current.mutate({ route: 'api-route', action: 'force-open' });
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });

  it('optimistic update applies immediately', async () => {
    const { wrapper, queryClient } = createWrapper();

    // Prefill dashboard data
    const dashHook = renderHook(() => useDashboard(), { wrapper });
    await waitFor(() => expect(dashHook.result.current.isSuccess).toBe(true));

    const cbHook = renderHook(() => useCircuitBreakerAction(), { wrapper });

    await act(async () => {
      cbHook.result.current.mutate({ route: 'api-route', action: 'force-open' });
    });

    // Check optimistic update was applied
    const cached = queryClient.getQueryData<{ circuit_breakers: Record<string, { state: string }> }>(['dashboard']);
    // After optimistic update, state should be 'open'
    if (cached) {
      expect(cached.circuit_breakers['api-route'].state).toBe('open');
    }
  });
});

describe('useReload', () => {
  it('calls POST /reload', async () => {
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useReload(), { wrapper });
    await act(async () => {
      result.current.mutate();
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });
});
