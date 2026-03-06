import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, type RenderOptions } from '@testing-library/react';
import { MemoryRouter, type MemoryRouterProps } from 'react-router-dom';
import { type ReactElement } from 'react';
import { PollingProvider } from '../context/PollingContext';

export function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
      },
    },
  });
}

interface ProviderOptions extends Omit<RenderOptions, 'wrapper'> {
  routerProps?: MemoryRouterProps;
  queryClient?: QueryClient;
}

export function renderWithProviders(
  ui: ReactElement,
  options: ProviderOptions = {},
) {
  const { routerProps, queryClient = createTestQueryClient(), ...renderOptions } = options;

  function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <PollingProvider>
          <MemoryRouter {...routerProps}>{children}</MemoryRouter>
        </PollingProvider>
      </QueryClientProvider>
    );
  }

  return { ...render(ui, { wrapper: Wrapper, ...renderOptions }), queryClient };
}

export function mockDashboardResponse(overrides: Record<string, unknown> = {}) {
  const base = {
    routes: [],
    backends: [],
    circuit_breakers: {},
    rate_limits: {},
    cache: {},
    uptime_seconds: 3600,
    total_requests: 1000,
    active_connections: 5,
  };
  return { ...base, ...overrides };
}
