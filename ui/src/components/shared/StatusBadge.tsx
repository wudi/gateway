import { clsx } from 'clsx';

type Status = 'healthy' | 'degraded' | 'down' | 'open' | 'closed' | 'half-open';

interface StatusBadgeProps {
  status: Status;
  label?: string;
}

const dotColors: Record<Status, string> = {
  healthy: 'bg-green-500',
  closed: 'bg-green-500',
  degraded: 'bg-amber-500',
  'half-open': 'bg-amber-500',
  down: 'bg-red-500',
  open: 'bg-red-500',
};

export function StatusBadge({ status, label }: StatusBadgeProps) {
  const displayLabel = label ?? status;
  return (
    <span
      className="inline-flex items-center gap-1.5 text-sm text-text-secondary"
      aria-label={displayLabel}
      data-status={status}
    >
      <span className={clsx('inline-block h-2 w-2 rounded-full', dotColors[status])} />
      {displayLabel}
    </span>
  );
}
