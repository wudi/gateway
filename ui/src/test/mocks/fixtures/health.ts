export interface HealthResponse {
  status: string;
  checks: Record<
    string,
    {
      status: string;
      message?: string;
      last_check: string;
    }
  >;
}

export const defaultHealth: HealthResponse = {
  status: 'healthy',
  checks: {
    backends: { status: 'healthy', last_check: '2025-01-01T00:00:00Z' },
    registry: { status: 'healthy', last_check: '2025-01-01T00:00:00Z' },
  },
};

export const degradedHealth: HealthResponse = {
  status: 'degraded',
  checks: {
    backends: {
      status: 'degraded',
      message: '1 of 4 backends unhealthy',
      last_check: '2025-01-01T00:00:00Z',
    },
    registry: { status: 'healthy', last_check: '2025-01-01T00:00:00Z' },
  },
};
