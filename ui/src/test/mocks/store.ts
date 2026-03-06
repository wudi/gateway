import { defaultDashboard } from './fixtures/dashboard';
import { defaultRoutes } from './fixtures/routes';
import { defaultHealth } from './fixtures/health';
import { defaultBackends } from './fixtures/backends';

export interface MockStore {
  dashboard: typeof defaultDashboard;
  routes: typeof defaultRoutes;
  health: typeof defaultHealth;
  backends: typeof defaultBackends;
  draining: boolean;
  circuitBreakerOverrides: Record<string, string>; // routeId -> state
  reloadHistory: Array<{ timestamp: string; success: boolean; message: string }>;
  listeners: Array<{
    id: string;
    address: string;
    protocol: string;
    active_connections: number;
  }>;
  certificates: Array<{
    hostname: string;
    issuer: string;
    not_after: string;
    days_remaining: number;
  }>;
  upstreams: Record<string, { backends: Array<{ url: string; healthy: boolean }> }>;
  rateLimits: Record<string, { rate: number; burst: number; current: number }>;
  trafficShaping: Record<string, { throttle_ms: number; bandwidth_limit: number }>;
  rules: { request_rules: unknown[]; response_rules: unknown[] };
  stats: { total_requests: number; uptime_seconds: number };
}

export function createInitialStore(): MockStore {
  return {
    dashboard: { ...defaultDashboard },
    routes: [...defaultRoutes],
    health: { ...defaultHealth },
    backends: [...defaultBackends],
    draining: false,
    circuitBreakerOverrides: {},
    reloadHistory: [
      { timestamp: '2025-01-01T00:00:00Z', success: true, message: 'initial load' },
    ],
    listeners: [
      { id: 'http-main', address: ':8080', protocol: 'http', active_connections: 12 },
      { id: 'https-main', address: ':8443', protocol: 'https', active_connections: 34 },
    ],
    certificates: [
      {
        hostname: 'example.com',
        issuer: "Let's Encrypt",
        not_after: '2026-06-01T00:00:00Z',
        days_remaining: 452,
      },
      {
        hostname: 'api.example.com',
        issuer: "Let's Encrypt",
        not_after: '2025-04-01T00:00:00Z',
        days_remaining: 26,
      },
    ],
    upstreams: {
      'api-pool': {
        backends: [
          { url: 'http://10.0.0.1:8080', healthy: true },
          { url: 'http://10.0.0.2:8080', healthy: true },
        ],
      },
    },
    rateLimits: {
      'api-route': { rate: 100, burst: 200, current: 42 },
    },
    trafficShaping: {
      'api-route': { throttle_ms: 0, bandwidth_limit: 0 },
    },
    rules: { request_rules: [], response_rules: [] },
    stats: { total_requests: 50000, uptime_seconds: 86400 },
  };
}

let store = createInitialStore();

export function getStore(): MockStore {
  return store;
}

export function resetStore(): void {
  store = createInitialStore();
}
