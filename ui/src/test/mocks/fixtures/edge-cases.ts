import type { RouteResponse } from './routes';
import type { DashboardResponse } from './dashboard';
import { defaultDashboard } from './dashboard';

export const allFeaturesRoute: RouteResponse = {
  id: 'all-features-route',
  path: '/all/*',
  methods: ['GET', 'POST', 'PUT', 'DELETE', 'PATCH'],
  backends: [
    { url: 'http://10.0.0.10:8080', weight: 50 },
    { url: 'http://10.0.0.11:8080', weight: 50 },
  ],
  features: {
    circuit_breaker: { enabled: true, max_failures: 10, timeout: '60s' },
    cache: { enabled: true, ttl: '120s', max_size: 5000 },
    retry: { enabled: true, max_retries: 5, backoff: 'exponential' },
    rate_limit: { enabled: true, rate: 500, burst: 1000 },
    websocket: { enabled: true },
    throttle: { enabled: true, rate: 50 },
  },
};

export const zeroBackendsRoute: RouteResponse = {
  id: 'no-backends-route',
  path: '/orphan',
  methods: ['GET'],
  backends: [],
  features: {},
};

export function createLargeRouteList(count: number): RouteResponse[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `route-${i}`,
    path: `/route-${i}/*`,
    methods: ['GET'],
    backends: [{ url: `http://10.0.${Math.floor(i / 256)}.${i % 256}:8080`, weight: 100 }],
    features: {},
  }));
}

export const expiredCertificate = {
  hostname: 'expired.example.com',
  issuer: "Let's Encrypt",
  not_after: '2025-01-01T00:00:00Z',
  days_remaining: 0,
};

export function createDashboardWithExpiredCert(): DashboardResponse {
  return { ...defaultDashboard };
}
