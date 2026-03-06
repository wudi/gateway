import { useDashboard, useHealth } from '../api/hooks';
import { StatusBadge } from '../components/shared/StatusBadge';
import { StatRow } from '../components/shared/StatRow';
import { Spinner } from '../components/shared/Spinner';
import { Link } from 'react-router-dom';

export function StatusPage() {
  const { data: dashboard, isLoading: dashLoading, isError: dashError } = useDashboard();
  const { data: health, isLoading: healthLoading, isError: healthError } = useHealth();

  if (dashLoading || healthLoading) {
    return <div className="flex justify-center py-12"><Spinner size="lg" /></div>;
  }

  if (dashError || healthError) {
    return (
      <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-400" role="alert">
        Failed to connect to admin API
      </div>
    );
  }

  if (!dashboard || !health) return null;

  const dashAny = dashboard as Record<string, unknown>;
  const problems: Array<{ type: string; route: string; message: string }> = [];

  // Check for open circuit breakers (real API uses "circuit-breakers", mock uses "circuit_breakers")
  const cbData = (dashAny['circuit-breakers'] ?? dashAny.circuit_breakers ?? null) as Record<string, { state: string }> | null;
  if (cbData) {
    for (const [routeId, cb] of Object.entries(cbData)) {
      if (cb.state === 'open') {
        problems.push({ type: 'Circuit Breaker', route: routeId, message: `Circuit breaker is ${cb.state}` });
      }
    }
  }

  // Check for unhealthy backends
  if (dashboard.backends && Array.isArray(dashboard.backends)) {
    for (const backend of dashboard.backends) {
      if (!backend.healthy) {
        problems.push({ type: 'Backend', route: backend.route_id, message: `Backend ${backend.url} is unhealthy` });
      }
    }
  }

  const hasProblems = problems.length > 0;

  // Route count — real API: {total, healthy}, mock: array
  const routeCount = Array.isArray(dashboard.routes)
    ? dashboard.routes.length
    : (dashboard.routes as { total?: number })?.total ?? 0;

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold text-text-primary">Status</h1>

      {!hasProblems ? (
        <div className="rounded-lg border border-green-500/30 bg-green-500/10 px-4 py-3 text-sm text-green-400">
          All systems operational
        </div>
      ) : (
        <div className="rounded-lg border border-border bg-bg-secondary">
          <div className="px-4 py-3 border-b border-border">
            <h2 className="text-sm font-medium text-text-primary">Problems ({problems.length})</h2>
          </div>
          <table className="w-full text-sm" role="table">
            <thead>
              <tr>
                <th className="px-4 py-2 text-left text-xs font-medium text-text-tertiary">Type</th>
                <th className="px-4 py-2 text-left text-xs font-medium text-text-tertiary">Route</th>
                <th className="px-4 py-2 text-left text-xs font-medium text-text-tertiary">Details</th>
                <th className="px-4 py-2 text-right text-xs font-medium text-text-tertiary" />
              </tr>
            </thead>
            <tbody>
              {problems.map((p, i) => (
                <tr key={i} className="border-t border-border">
                  <td className="px-4 py-2 text-text-primary">{p.type}</td>
                  <td className="px-4 py-2 text-text-primary">{p.route}</td>
                  <td className="px-4 py-2 text-text-secondary">{p.message}</td>
                  <td className="px-4 py-2 text-right">
                    <Link
                      to={`/routes?route=${p.route}`}
                      className="text-blue-400 hover:text-blue-300 transition-colors duration-150"
                    >
                      View
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <StatRow
        items={[
          { label: 'Uptime', value: (dashAny.uptime as string) ?? 'N/A' },
          { label: 'Routes', value: routeCount },
          { label: 'Listeners', value: (dashAny.listeners as number) ?? 0 },
        ]}
      />

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Health</h2>
        <StatusBadge status={health.status as 'healthy' | 'degraded' | 'down'} />
      </div>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Recent Events</h2>
        <div className="text-sm text-text-secondary">System started</div>
      </div>
    </div>
  );
}
