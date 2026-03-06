import { useState } from 'react';
import { useReload, useDrain, useReloadStatus } from '../api/hooks';
import { ConfirmModal } from '../components/shared/ConfirmModal';
import { ActionButton } from '../components/shared/ActionButton';
import { Spinner } from '../components/shared/Spinner';

export function OperationsPage() {
  const reload = useReload();
  const drain = useDrain();
  const reloadStatus = useReloadStatus();
  const [drainConfirm, setDrainConfirm] = useState(false);
  const [reloadResult, setReloadResult] = useState<'success' | 'error' | null>(null);
  const [maintenanceEnabled, setMaintenanceEnabled] = useState(false);
  const [maintenanceError, setMaintenanceError] = useState(false);

  const handleReload = async () => {
    try {
      await reload.mutateAsync();
      setReloadResult('success');
      setTimeout(() => setReloadResult(null), 3000);
    } catch {
      setReloadResult('error');
    }
  };

  const handleMaintenanceToggle = () => {
    const prev = maintenanceEnabled;
    setMaintenanceEnabled(!prev);
    // Simulate potential error for testing rollback
    if (maintenanceError) {
      setTimeout(() => setMaintenanceEnabled(prev), 100);
    }
  };

  const reloadHistory = (reloadStatus.data ?? []) as Array<{
    timestamp: string;
    success: boolean;
    message: string;
  }>;

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold text-text-primary">Operations</h1>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Config Reload</h2>
        <ActionButton label="Reload Config" onClick={handleReload} />
        {reloadResult === 'success' && (
          <div className="mt-2 rounded-md bg-green-500/10 px-3 py-2 text-sm text-green-400" role="status">
            Configuration reloaded successfully
          </div>
        )}
        {reloadResult === 'error' && (
          <div className="mt-2 rounded-md bg-red-500/10 px-3 py-2 text-sm text-red-400" role="alert">
            Failed to reload configuration
          </div>
        )}
      </div>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Reload History</h2>
        {reloadStatus.isLoading ? (
          <Spinner />
        ) : (
          <table className="w-full text-sm" role="table">
            <thead>
              <tr>
                <th className="text-left text-xs text-text-tertiary pb-2">Time</th>
                <th className="text-left text-xs text-text-tertiary pb-2">Status</th>
                <th className="text-left text-xs text-text-tertiary pb-2">Message</th>
              </tr>
            </thead>
            <tbody>
              {reloadHistory.map((entry, i) => (
                <tr key={i} className="border-t border-border">
                  <td className="py-2 text-text-secondary">{entry.timestamp}</td>
                  <td className="py-2">
                    <span className={entry.success ? 'text-green-400' : 'text-red-400'}>
                      {entry.success ? 'Success' : 'Failed'}
                    </span>
                  </td>
                  <td className="py-2 text-text-secondary">{entry.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Drain</h2>
        <button
          type="button"
          onClick={() => setDrainConfirm(true)}
          className="px-3 py-1.5 text-sm rounded-md bg-amber-500/10 text-amber-400 hover:bg-amber-500/20 transition-colors duration-150"
        >
          {drain.data && (drain.data as { draining: boolean }).draining ? 'Cancel Drain' : 'Start Drain'}
        </button>
        {drain.isSuccess && (drain.data as { draining: boolean }).draining && (
          <span className="ml-3 text-sm text-amber-400">Draining</span>
        )}
      </div>

      <div>
        <h2 className="text-sm font-medium text-text-primary mb-3">Maintenance Mode</h2>
        <button
          type="button"
          onClick={handleMaintenanceToggle}
          className={`px-3 py-1.5 text-sm rounded-md transition-colors duration-150 ${
            maintenanceEnabled
              ? 'bg-amber-500/20 text-amber-400'
              : 'bg-bg-elevated text-text-primary hover:bg-white/10'
          }`}
        >
          {maintenanceEnabled ? 'Disable Maintenance' : 'Enable Maintenance'}
        </button>
      </div>

      {drainConfirm && (
        <ConfirmModal
          title="Toggle Drain Mode"
          description="This will start draining connections. New requests will be rejected."
          confirmLabel="Confirm"
          requireTypedConfirmation="drain"
          onConfirm={() => {
            drain.mutate();
            setDrainConfirm(false);
          }}
          onCancel={() => setDrainConfirm(false)}
          isPending={drain.isPending}
        />
      )}
    </div>
  );
}
