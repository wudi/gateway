interface StatRowProps {
  items: Array<{ label: string; value: string | number }>;
}

export function StatRow({ items }: StatRowProps) {
  return (
    <div className="flex gap-8 px-4 py-3 bg-bg-secondary rounded-lg border border-border" role="row">
      {items.map((item) => (
        <div key={item.label} className="flex flex-col">
          <span className="text-xs text-text-tertiary">{item.label}</span>
          <span className="text-sm text-text-primary tabular-nums" data-numeric="true">
            {item.value}
          </span>
        </div>
      ))}
    </div>
  );
}
