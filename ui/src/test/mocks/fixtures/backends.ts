export interface BackendResponse {
  url: string;
  route_id: string;
  healthy: boolean;
  response_time_ms: number;
  active_connections: number;
  total_requests: number;
}

export const defaultBackends: BackendResponse[] = [
  {
    url: 'http://10.0.0.1:8080',
    route_id: 'api-route',
    healthy: true,
    response_time_ms: 45,
    active_connections: 5,
    total_requests: 7500,
  },
  {
    url: 'http://10.0.0.2:8080',
    route_id: 'api-route',
    healthy: true,
    response_time_ms: 52,
    active_connections: 4,
    total_requests: 7500,
  },
  {
    url: 'http://10.0.0.3:8080',
    route_id: 'web-route',
    healthy: true,
    response_time_ms: 30,
    active_connections: 3,
    total_requests: 8000,
  },
  {
    url: 'http://10.0.0.4:8080',
    route_id: 'plain-route',
    healthy: true,
    response_time_ms: 20,
    active_connections: 1,
    total_requests: 500,
  },
];

export const backendsWithUnhealthy: BackendResponse[] = defaultBackends.map((b) =>
  b.url === 'http://10.0.0.2:8080'
    ? { ...b, healthy: false, response_time_ms: 0, active_connections: 0 }
    : b,
);
