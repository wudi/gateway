export interface RouteResponse {
  id: string;
  path: string;
  methods: string[];
  backends: Array<{ url: string; weight: number }>;
  features: {
    circuit_breaker?: { enabled: boolean; max_failures: number; timeout: string };
    cache?: { enabled: boolean; ttl: string; max_size: number };
    retry?: { enabled: boolean; max_retries: number; backoff: string };
    rate_limit?: { enabled: boolean; rate: number; burst: number };
    websocket?: { enabled: boolean };
    throttle?: { enabled: boolean; rate: number };
  };
}

export const defaultRoutes: RouteResponse[] = [
  {
    id: 'api-route',
    path: '/api/*',
    methods: ['GET', 'POST', 'PUT', 'DELETE'],
    backends: [
      { url: 'http://10.0.0.1:8080', weight: 50 },
      { url: 'http://10.0.0.2:8080', weight: 50 },
    ],
    features: {
      circuit_breaker: { enabled: true, max_failures: 5, timeout: '30s' },
      cache: { enabled: true, ttl: '60s', max_size: 1000 },
      retry: { enabled: true, max_retries: 3, backoff: 'exponential' },
      rate_limit: { enabled: true, rate: 100, burst: 200 },
    },
  },
  {
    id: 'web-route',
    path: '/web/*',
    methods: ['GET'],
    backends: [{ url: 'http://10.0.0.3:8080', weight: 100 }],
    features: {
      retry: { enabled: true, max_retries: 2, backoff: 'linear' },
    },
  },
  {
    id: 'plain-route',
    path: '/plain',
    methods: ['GET'],
    backends: [{ url: 'http://10.0.0.4:8080', weight: 100 }],
    features: {},
  },
];
