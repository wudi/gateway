export interface DashboardResponse {
  routes: Array<{
    id: string;
    path: string;
    backends: number;
    healthy_backends: number;
    total_requests: number;
    error_rate: number;
    features: string[];
  }>;
  backends: Array<{
    url: string;
    route_id: string;
    healthy: boolean;
    response_time_ms: number;
  }>;
  circuit_breakers: Record<
    string,
    {
      state: string;
      failures: number;
      successes: number;
      consecutive_failures: number;
    }
  >;
  rate_limits: Record<string, { rate: number; burst: number; current: number }>;
  cache: Record<string, { hits: number; misses: number; size: number; evictions: number }>;
  uptime_seconds: number;
  total_requests: number;
  active_connections: number;
}

export const defaultDashboard: DashboardResponse = {
  routes: [
    {
      id: 'api-route',
      path: '/api/*',
      backends: 2,
      healthy_backends: 2,
      total_requests: 15000,
      error_rate: 0.02,
      features: ['CB', 'CA', 'RT', 'RL'],
    },
    {
      id: 'web-route',
      path: '/web/*',
      backends: 1,
      healthy_backends: 1,
      total_requests: 8000,
      error_rate: 0.0,
      features: ['RT'],
    },
    {
      id: 'plain-route',
      path: '/plain',
      backends: 1,
      healthy_backends: 1,
      total_requests: 500,
      error_rate: 0.0,
      features: [],
    },
  ],
  backends: [
    { url: 'http://10.0.0.1:8080', route_id: 'api-route', healthy: true, response_time_ms: 45 },
    { url: 'http://10.0.0.2:8080', route_id: 'api-route', healthy: true, response_time_ms: 52 },
    { url: 'http://10.0.0.3:8080', route_id: 'web-route', healthy: true, response_time_ms: 30 },
    { url: 'http://10.0.0.4:8080', route_id: 'plain-route', healthy: true, response_time_ms: 20 },
  ],
  circuit_breakers: {
    'api-route': { state: 'closed', failures: 3, successes: 14997, consecutive_failures: 0 },
  },
  rate_limits: {
    'api-route': { rate: 100, burst: 200, current: 42 },
  },
  cache: {
    'api-route': { hits: 5000, misses: 10000, size: 256, evictions: 100 },
  },
  uptime_seconds: 86400,
  total_requests: 23500,
  active_connections: 15,
};

export function createDegradedDashboard(): DashboardResponse {
  return {
    ...defaultDashboard,
    routes: defaultDashboard.routes.map((r) =>
      r.id === 'api-route'
        ? { ...r, healthy_backends: 1, error_rate: 0.15 }
        : r,
    ),
    backends: defaultDashboard.backends.map((b) =>
      b.url === 'http://10.0.0.2:8080' ? { ...b, healthy: false } : b,
    ),
    circuit_breakers: {
      'api-route': { state: 'open', failures: 150, successes: 14850, consecutive_failures: 5 },
    },
  };
}
