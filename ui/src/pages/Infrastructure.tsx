import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useBackends, useListeners, useCertificates, useUpstreams } from '../api/hooks';
import { DataTable, type Column } from '../components/shared/DataTable';
import { StatusBadge } from '../components/shared/StatusBadge';
import { Spinner } from '../components/shared/Spinner';

type Tab = 'backends' | 'listeners' | 'certificates' | 'upstreams' | 'cluster';

interface BackendItem {
  url: string;
  route_id: string;
  healthy: boolean;
  response_time_ms: number;
  active_connections: number;
  total_requests: number;
}

interface ListenerItem {
  id: string;
  address: string;
  protocol: string;
  active_connections: number;
}

interface CertItem {
  hostname: string;
  issuer: string;
  not_after: string;
  days_remaining: number;
}

export function InfrastructurePage() {
  const [searchParams] = useSearchParams();
  const initialTab = (searchParams.get('tab') as Tab) ?? 'backends';
  const [activeTab, setActiveTab] = useState<Tab>(initialTab);

  const backends = useBackends();
  const listeners = useListeners();
  const certificates = useCertificates();
  const upstreams = useUpstreams();

  const tabs: Array<{ id: Tab; label: string }> = [
    { id: 'backends', label: 'Backends' },
    { id: 'listeners', label: 'Listeners' },
    { id: 'certificates', label: 'Certificates' },
    { id: 'upstreams', label: 'Upstreams' },
  ];

  const backendColumns: Column<BackendItem>[] = [
    {
      key: 'url',
      header: 'URL',
      render: (row) => row.url,
    },
    {
      key: 'route',
      header: 'Route',
      render: (row) => row.route_id,
    },
    {
      key: 'status',
      header: 'Status',
      render: (row) => <StatusBadge status={row.healthy ? 'healthy' : 'down'} />,
      sortable: true,
      sortFn: (a, b) => (a.healthy === b.healthy ? 0 : a.healthy ? 1 : -1),
    },
    {
      key: 'response_time',
      header: 'Response Time',
      render: (row) => `${row.response_time_ms}ms`,
      numeric: true,
    },
  ];

  const listenerColumns: Column<ListenerItem>[] = [
    { key: 'id', header: 'ID', render: (row) => row.id },
    { key: 'address', header: 'Address', render: (row) => row.address },
    { key: 'protocol', header: 'Protocol', render: (row) => row.protocol },
    {
      key: 'connections',
      header: 'Connections',
      render: (row) => row.active_connections,
      numeric: true,
    },
  ];

  const certColumns: Column<CertItem>[] = [
    { key: 'hostname', header: 'Hostname', render: (row) => row.hostname },
    { key: 'issuer', header: 'Issuer', render: (row) => row.issuer },
    {
      key: 'days_remaining',
      header: 'Expires',
      render: (row) => {
        if (row.days_remaining <= 0) {
          return <span className="text-red-400 font-medium">Expired</span>;
        }
        if (row.days_remaining <= 30) {
          return <span className="text-amber-400">{row.days_remaining}d remaining</span>;
        }
        return <span>{row.days_remaining}d remaining</span>;
      },
      numeric: true,
      sortable: true,
      sortFn: (a, b) => a.days_remaining - b.days_remaining,
    },
  ];

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold text-text-primary">Infrastructure</h1>

      <div className="flex gap-1 border-b border-border">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            type="button"
            onClick={() => setActiveTab(tab.id)}
            className={`px-4 py-2 text-sm transition-colors duration-150 border-b-2 ${
              activeTab === tab.id
                ? 'border-text-primary text-text-primary'
                : 'border-transparent text-text-secondary hover:text-text-primary'
            }`}
            role="tab"
            aria-selected={activeTab === tab.id}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {activeTab === 'backends' && (
        backends.isLoading ? (
          <Spinner />
        ) : (
          <DataTable
            data={((backends.data as BackendItem[]) ?? []).sort((a, b) =>
              a.healthy === b.healthy ? 0 : a.healthy ? 1 : -1,
            )}
            columns={backendColumns}
            rowKey={(r) => r.url}
            emptyMessage="No backends configured."
          />
        )
      )}

      {activeTab === 'listeners' && (
        listeners.isLoading ? (
          <Spinner />
        ) : (
          <DataTable
            data={(listeners.data as ListenerItem[]) ?? []}
            columns={listenerColumns}
            rowKey={(r) => r.id}
            emptyMessage="No listeners configured."
          />
        )
      )}

      {activeTab === 'certificates' && (
        certificates.isLoading ? (
          <Spinner />
        ) : (
          <DataTable
            data={(certificates.data as CertItem[]) ?? []}
            columns={certColumns}
            rowKey={(r) => r.hostname}
            emptyMessage="No certificates configured."
          />
        )
      )}

      {activeTab === 'upstreams' && (
        upstreams.isLoading ? (
          <Spinner />
        ) : (
          <div className="text-sm text-text-primary">
            {upstreams.data
              ? Object.entries(upstreams.data as Record<string, { backends: Array<{ url: string; healthy: boolean }> }>).map(
                  ([name, upstream]) => (
                    <div key={name} className="mb-4">
                      <h3 className="font-medium mb-1">{name}</h3>
                      <ul>
                        {upstream.backends.map((b) => (
                          <li key={b.url}>
                            {b.url} — <StatusBadge status={b.healthy ? 'healthy' : 'down'} />
                          </li>
                        ))}
                      </ul>
                    </div>
                  ),
                )
              : 'No upstreams configured.'}
          </div>
        )
      )}
    </div>
  );
}
