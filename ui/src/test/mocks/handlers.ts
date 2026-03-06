import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { getStore, resetStore } from './store';

const handlers = [
  // GET endpoints
  http.get('/dashboard', () => {
    const store = getStore();
    const dashboard = { ...store.dashboard };
    // Apply circuit breaker overrides
    for (const [routeId, state] of Object.entries(store.circuitBreakerOverrides)) {
      if (dashboard.circuit_breakers[routeId]) {
        dashboard.circuit_breakers[routeId] = {
          ...dashboard.circuit_breakers[routeId],
          state,
        };
      }
    }
    return HttpResponse.json(dashboard);
  }),

  http.get('/health', () => {
    return HttpResponse.json(getStore().health);
  }),

  http.get('/routes', () => {
    return HttpResponse.json(getStore().routes);
  }),

  http.get('/backends', () => {
    return HttpResponse.json(getStore().backends);
  }),

  http.get('/listeners', () => {
    return HttpResponse.json(getStore().listeners);
  }),

  http.get('/certificates', () => {
    return HttpResponse.json(getStore().certificates);
  }),

  http.get('/circuit-breakers', () => {
    const store = getStore();
    return HttpResponse.json(store.dashboard.circuit_breakers);
  }),

  http.get('/rate-limits', () => {
    return HttpResponse.json(getStore().rateLimits);
  }),

  http.get('/traffic-shaping', () => {
    return HttpResponse.json(getStore().trafficShaping);
  }),

  http.get('/rules', () => {
    return HttpResponse.json(getStore().rules);
  }),

  http.get('/drain', () => {
    return HttpResponse.json({ draining: getStore().draining });
  }),

  http.get('/reload/status', () => {
    return HttpResponse.json(getStore().reloadHistory);
  }),

  http.get('/upstreams', () => {
    return HttpResponse.json(getStore().upstreams);
  }),

  http.get('/stats', () => {
    return HttpResponse.json(getStore().stats);
  }),

  // POST endpoints (mutations)
  http.post('/drain', async () => {
    const store = getStore();
    store.draining = !store.draining;
    return HttpResponse.json({ draining: store.draining });
  }),

  http.post('/reload', async () => {
    const store = getStore();
    const entry = {
      timestamp: new Date().toISOString(),
      success: true,
      message: 'config reloaded',
    };
    store.reloadHistory.push(entry);
    return HttpResponse.json(entry);
  }),

  http.post('/cache/purge', async () => {
    const store = getStore();
    for (const key of Object.keys(store.dashboard.cache)) {
      store.dashboard.cache[key] = {
        ...store.dashboard.cache[key],
        size: 0,
        evictions: 0,
      };
    }
    return HttpResponse.json({ purged: true });
  }),

  http.post('/circuit-breakers/:route/:action', async ({ params }) => {
    const { route, action } = params as { route: string; action: string };
    const store = getStore();
    if (action === 'force-open') {
      store.circuitBreakerOverrides[route] = 'open';
    } else if (action === 'reset') {
      store.circuitBreakerOverrides[route] = 'closed';
    }
    return HttpResponse.json({
      route,
      action,
      state: store.circuitBreakerOverrides[route] ?? 'closed',
    });
  }),
];

export const server = setupServer(...handlers);

// Re-export resetStore for use in afterEach
export { resetStore };
