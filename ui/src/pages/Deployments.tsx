import { useState } from 'react';
import { useDashboard } from '../api/hooks';
import { ConfirmModal } from '../components/shared/ConfirmModal';
import { Spinner } from '../components/shared/Spinner';

export function DeploymentsPage() {
  const { isLoading } = useDashboard();
  const [promoteConfirm, setPromoteConfirm] = useState<string | null>(null);

  if (isLoading) {
    return <div className="flex justify-center py-12"><Spinner size="lg" /></div>;
  }

  // Deployments data would come from /dashboard or dedicated endpoints
  const hasDeployments = false;

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold text-text-primary">Deployments</h1>

      {!hasDeployments ? (
        <p className="text-sm text-text-secondary">No active deployments.</p>
      ) : (
        <>
          <div>
            <h2 className="text-sm font-medium text-text-primary mb-3">Active Canaries</h2>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setPromoteConfirm('canary-1')}
                className="px-3 py-1.5 text-sm rounded-md bg-green-500/10 text-green-400 hover:bg-green-500/20 transition-colors duration-150"
              >
                Promote
              </button>
              <button
                type="button"
                className="px-3 py-1.5 text-sm rounded-md bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors duration-150"
              >
                Rollback
              </button>
            </div>
          </div>

          <div>
            <h2 className="text-sm font-medium text-text-primary mb-3">Blue-Green</h2>
          </div>

          <div>
            <h2 className="text-sm font-medium text-text-primary mb-3">A/B Tests</h2>
          </div>

          <div>
            <h2 className="text-sm font-medium text-text-primary mb-3">Traffic Splits</h2>
          </div>
        </>
      )}

      {promoteConfirm && (
        <ConfirmModal
          title="Promote Canary"
          description={`Promote canary deployment "${promoteConfirm}" to production.`}
          confirmLabel="Promote"
          onConfirm={() => setPromoteConfirm(null)}
          onCancel={() => setPromoteConfirm(null)}
        />
      )}
    </div>
  );
}
