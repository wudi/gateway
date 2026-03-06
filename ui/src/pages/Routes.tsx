import { useState, useEffect, useCallback, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useDashboard, useRoutes, useCircuitBreakerAction, useCachePurge } from '../api/hooks';
import { DataTable, type Column } from '../components/shared/DataTable';
import { StatusBadge } from '../components/shared/StatusBadge';
import { ConfirmModal } from '../components/shared/ConfirmModal';
import { Spinner } from '../components/shared/Spinner';
import { X } from 'lucide-react';

interface ApiRoute {
  id: string;
  path: string;
  path_prefix: boolean;
  backends: number;
  methods?: string[];
}

export function RoutesPage() {
  const [searchParams] = useSearchParams();
  const [selectedRouteId, setSelectedRouteId] = useState<string | null>(
    searchParams.get('route'),
  );
  const [cbConfirm, setCbConfirm] = useState<{ route: string; action: string } | null>(null);
  const [cacheConfirm, setCacheConfirm] = useState<string | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  const { data: dashboard } = useDashboard();
  const { data: routes, isLoading } = useRoutes();
  const cbAction = useCircuitBreakerAction();
  const cachePurge = useCachePurge();

  const closePanel = useCallback(() => {
    setSelectedRouteId(null);
    if (previousFocusRef.current) {
      previousFocusRef.current.focus();
    }
  }, []);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && selectedRouteId) {
        closePanel();
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [selectedRouteId, closePanel]);

  useEffect(() => {
    if (selectedRouteId && panelRef.current) {
      const firstFocusable = panelRef.current.querySelector<HTMLElement>('button, [tabindex]');
      firstFocusable?.focus();
    }
  }, [selectedRouteId]);

  if (isLoading) {
    return <div className="flex justify-center py-12"><Spinner size="lg" /></div>;
  }

  const routeList = (routes ?? []) as unknown as ApiRoute[];

  // Extract dashboard feature data (real API uses hyphenated keys)
  const dashAny = dashboard as Record<string, unknown> | undefined;
  const cbMap = (dashAny?.['circuit-breakers'] ?? dashAny?.circuit_breakers ?? {}) as Record<string, { state: string }>;
  const cacheMap = (dashAny?.cache ?? {}) as Record<string, { hits: number; misses: number; size: number; evictions: number }>;
  const retryMap = (dashAny?.retries ?? {}) as Record<string, unknown>;
  const rlMap = (dashAny?.['rate-limits'] ?? dashAny?.rate_limits ?? {}) as Record<string, unknown>;

  // Derive feature badges per route
  function getFeatures(routeId: string): string[] {
    const features: string[] = [];
    if (cbMap[routeId]) features.push('CB');
    if (cacheMap[routeId]) features.push('CA');
    if (retryMap[routeId]) features.push('RT');
    if (rlMap[routeId]) features.push('RL');
    return features;
  }

  const featureColors: Record<string, string> = {
    CB: 'bg-green-500/20 text-green-400',
    RT: 'bg-blue-500/20 text-blue-400',
    CA: 'bg-purple-500/20 text-purple-400',
    RL: 'bg-amber-500/20 text-amber-400',
    WS: 'bg-cyan-500/20 text-cyan-400',
    TH: 'bg-orange-500/20 text-orange-400',
  };

  const columns: Column<ApiRoute>[] = [
    {
      key: 'id',
      header: 'Route ID',
      render: (row) => row.id,
      sortable: true,
      sortFn: (a, b) => a.id.localeCompare(b.id),
    },
    {
      key: 'path',
      header: 'Path',
      render: (row) => <span className="text-text-secondary">{row.path}</span>,
    },
    {
      key: 'backends',
      header: 'Backends',
      render: (row) => <span>{row.backends}</span>,
      numeric: true,
    },
    {
      key: 'features',
      header: 'Features',
      render: (row) => {
        const features = getFeatures(row.id);
        return (
          <div className="flex gap-1">
            {features.map((f) => (
              <span
                key={f}
                className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium ${featureColors[f] ?? 'bg-gray-500/20 text-gray-400'}`}
              >
                {f}
              </span>
            ))}
          </div>
        );
      },
    },
  ];

  const selectedRoute = routeList.find((r) => r.id === selectedRouteId);
  const cbData = selectedRouteId ? cbMap[selectedRouteId] : undefined;
  const cacheData = selectedRouteId ? cacheMap[selectedRouteId] : undefined;

  return (
    <div className="flex gap-0">
      <div className={selectedRouteId ? 'w-[60%]' : 'w-full'}>
        <h1 className="text-xl font-semibold text-text-primary mb-4">Routes</h1>
        <DataTable
          data={routeList}
          columns={columns}
          rowKey={(r) => r.id}
          selectedKey={selectedRouteId ?? undefined}
          onSelect={(row) => {
            previousFocusRef.current = document.activeElement as HTMLElement;
            setSelectedRouteId(row.id);
          }}
          searchable
          searchFn={(row, q) =>
            row.id.toLowerCase().includes(q.toLowerCase()) ||
            row.path.toLowerCase().includes(q.toLowerCase())
          }
          emptyMessage="No routes configured."
        />
      </div>

      {selectedRouteId && selectedRoute && (
        <div
          ref={panelRef}
          className="w-[40%] border-l border-border bg-bg-secondary p-4 overflow-y-auto"
          role="complementary"
          aria-label={`Details for ${selectedRouteId}`}
        >
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-semibold text-text-primary">{selectedRouteId}</h2>
            <button
              type="button"
              onClick={closePanel}
              className="text-text-tertiary hover:text-text-primary transition-colors duration-150"
              aria-label="Close detail panel"
            >
              <X className="h-5 w-5" />
            </button>
          </div>

          <div className="space-y-4">
            <div>
              <h3 className="text-xs text-text-tertiary mb-1">Path</h3>
              <p className="text-sm text-text-primary">{selectedRoute.path}</p>
            </div>

            {selectedRoute.methods && selectedRoute.methods.length > 0 && (
              <div>
                <h3 className="text-xs text-text-tertiary mb-1">Methods</h3>
                <p className="text-sm text-text-primary">{selectedRoute.methods.join(', ')}</p>
              </div>
            )}

            <div>
              <h3 className="text-xs text-text-tertiary mb-1">Backends</h3>
              <p className="text-sm text-text-primary">{selectedRoute.backends} backend(s)</p>
            </div>

            {cbData && (
              <div>
                <h3 className="text-xs text-text-tertiary mb-1">Circuit Breaker</h3>
                <div className="space-y-2">
                  <StatusBadge status={cbData.state as 'open' | 'closed' | 'half-open'} />
                  <div className="flex gap-2">
                    <button
                      type="button"
                      onClick={() => setCbConfirm({ route: selectedRouteId, action: 'force-open' })}
                      className="px-2 py-1 text-xs rounded bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors duration-150"
                    >
                      Force Open
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        cbAction.mutate({ route: selectedRouteId, action: 'reset' })
                      }
                      className="px-2 py-1 text-xs rounded bg-bg-elevated text-text-primary hover:bg-white/10 transition-colors duration-150"
                    >
                      Reset
                    </button>
                  </div>
                </div>
              </div>
            )}

            {cacheData && (
              <div>
                <h3 className="text-xs text-text-tertiary mb-1">Cache</h3>
                <div className="text-sm text-text-primary space-y-1">
                  <p>Hits: {cacheData.hits} / Misses: {cacheData.misses}</p>
                  <p>Size: {cacheData.size} / Evictions: {cacheData.evictions}</p>
                </div>
                <button
                  type="button"
                  onClick={() => setCacheConfirm(selectedRouteId)}
                  className="mt-2 px-2 py-1 text-xs rounded bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors duration-150"
                >
                  Purge Cache
                </button>
              </div>
            )}
          </div>
        </div>
      )}

      {cbConfirm && (
        <ConfirmModal
          title="Force Open Circuit Breaker"
          description={`This will force-open the circuit breaker for route "${cbConfirm.route}", blocking all traffic.`}
          confirmLabel="Force Open"
          requireTypedConfirmation={cbConfirm.route}
          onConfirm={() => {
            cbAction.mutate(cbConfirm);
            setCbConfirm(null);
          }}
          onCancel={() => setCbConfirm(null)}
          isPending={cbAction.isPending}
          error={cbAction.error?.message}
        />
      )}

      {cacheConfirm && (
        <ConfirmModal
          title="Purge Cache"
          description={`This will purge all cached entries for route "${cacheConfirm}".`}
          confirmLabel="Purge"
          onConfirm={() => {
            cachePurge.mutate();
            setCacheConfirm(null);
          }}
          onCancel={() => setCacheConfirm(null)}
          isPending={cachePurge.isPending}
        />
      )}
    </div>
  );
}
