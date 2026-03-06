import { useRateLimits, useTrafficShaping } from '../api/hooks';
import { Spinner } from '../components/shared/Spinner';
import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';

interface CollapsibleSectionProps {
  title: string;
  configured: boolean;
  children: React.ReactNode;
}

function CollapsibleSection({ title, configured, children }: CollapsibleSectionProps) {
  const [expanded, setExpanded] = useState(configured);
  return (
    <div className="border border-border rounded-lg">
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center justify-between px-4 py-3 text-sm text-left transition-colors duration-150 hover:bg-bg-elevated/50"
      >
        <span className="text-text-primary font-medium">
          {title}
          {!configured && <span className="ml-2 text-text-tertiary">(not configured)</span>}
        </span>
        {expanded ? (
          <ChevronDown className="h-4 w-4 text-text-tertiary" />
        ) : (
          <ChevronRight className="h-4 w-4 text-text-tertiary" />
        )}
      </button>
      {expanded && <div className="px-4 pb-4">{children}</div>}
    </div>
  );
}

export function TrafficControlPage() {
  const rateLimits = useRateLimits();
  const trafficShaping = useTrafficShaping();

  if (rateLimits.isLoading || trafficShaping.isLoading) {
    return <div className="flex justify-center py-12"><Spinner size="lg" /></div>;
  }

  const rlData = (rateLimits.data ?? {}) as Record<string, Record<string, unknown>>;
  const tsData = (trafficShaping.data ?? {}) as Record<string, Record<string, unknown>>;

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold text-text-primary">Traffic Control</h1>

      <CollapsibleSection title="Rate Limits" configured={Object.keys(rlData).length > 0}>
        <table className="w-full text-sm" role="table">
          <thead>
            <tr>
              <th className="text-left text-xs text-text-tertiary pb-2">Route</th>
              <th className="text-left text-xs text-text-tertiary pb-2">Rate</th>
              <th className="text-left text-xs text-text-tertiary pb-2">Burst</th>
              <th className="text-left text-xs text-text-tertiary pb-2">Current</th>
            </tr>
          </thead>
          <tbody>
            {Object.entries(rlData).map(([route, rl]) => (
              <tr key={route}>
                <td className="py-1 text-text-primary">{route}</td>
                <td className="py-1 tabular-nums">{String(rl.algorithm ?? rl.rate ?? 'N/A')}</td>
                <td className="py-1 tabular-nums">{String(rl.mode ?? rl.burst ?? 'N/A')}</td>
                <td className="py-1 tabular-nums">{String(rl.current ?? '-')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </CollapsibleSection>

      <CollapsibleSection title="Throttle & Shaping" configured={Object.keys(tsData).length > 0}>
        <table className="w-full text-sm" role="table">
          <thead>
            <tr>
              <th className="text-left text-xs text-text-tertiary pb-2">Route</th>
              <th className="text-left text-xs text-text-tertiary pb-2">Throttle (ms)</th>
              <th className="text-left text-xs text-text-tertiary pb-2">Bandwidth Limit</th>
            </tr>
          </thead>
          <tbody>
            {Object.entries(tsData).map(([route, ts]) => (
              <tr key={route}>
                <td className="py-1 text-text-primary">{route}</td>
                <td className="py-1 tabular-nums">{String(ts.throttle_ms ?? ts.delay ?? '-')}</td>
                <td className="py-1 tabular-nums">{String(ts.bandwidth_limit ?? '-')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </CollapsibleSection>

      <CollapsibleSection title="Load Shedding" configured={false}>
        <p className="text-sm text-text-secondary">No load shedding rules configured.</p>
      </CollapsibleSection>

      <CollapsibleSection title="Adaptive Concurrency" configured={false}>
        <p className="text-sm text-text-secondary">No adaptive concurrency limits configured.</p>
      </CollapsibleSection>
    </div>
  );
}
