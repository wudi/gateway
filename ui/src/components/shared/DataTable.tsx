import { useState, useCallback, useRef, useEffect, type ReactNode } from 'react';
import { clsx } from 'clsx';
import { EmptyState } from './EmptyState';

export interface Column<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  numeric?: boolean;
  sortable?: boolean;
  sortFn?: (a: T, b: T) => number;
}

interface DataTableProps<T> {
  data: T[];
  columns: Column<T>[];
  rowKey: (row: T) => string;
  onSelect?: (row: T) => void;
  selectedKey?: string;
  searchable?: boolean;
  searchFn?: (row: T, query: string) => boolean;
  emptyMessage?: string;
}

export function DataTable<T>({
  data,
  columns,
  rowKey,
  onSelect,
  selectedKey,
  searchable = false,
  searchFn,
  emptyMessage = 'No data available.',
}: DataTableProps<T>) {
  const [search, setSearch] = useState('');
  const [sortCol, setSortCol] = useState<string | null>(null);
  const [sortAsc, setSortAsc] = useState(true);
  const [highlightIdx, setHighlightIdx] = useState(-1);
  const tableRef = useRef<HTMLTableElement>(null);

  let filtered = data;
  if (search && searchFn) {
    filtered = data.filter((row) => searchFn(row, search));
  }

  if (sortCol) {
    const col = columns.find((c) => c.key === sortCol);
    if (col?.sortFn) {
      filtered = [...filtered].sort((a, b) => (sortAsc ? col.sortFn!(a, b) : col.sortFn!(b, a)));
    }
  }

  const handleHeaderClick = useCallback(
    (col: Column<T>) => {
      if (!col.sortable) return;
      if (sortCol === col.key) {
        setSortAsc(!sortAsc);
      } else {
        setSortCol(col.key);
        setSortAsc(true);
      }
    },
    [sortCol, sortAsc],
  );

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (!tableRef.current?.contains(document.activeElement) && document.activeElement !== tableRef.current) return;
      if (e.key === 'j') {
        e.preventDefault();
        setHighlightIdx((prev) => Math.min(prev + 1, filtered.length - 1));
      } else if (e.key === 'k') {
        e.preventDefault();
        setHighlightIdx((prev) => Math.max(prev - 1, 0));
      } else if (e.key === 'Enter' && highlightIdx >= 0 && highlightIdx < filtered.length) {
        e.preventDefault();
        onSelect?.(filtered[highlightIdx]);
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [filtered, highlightIdx, onSelect]);

  if (filtered.length === 0 && !search) {
    return <EmptyState message={emptyMessage} />;
  }

  return (
    <div>
      {searchable && (
        <div className="mb-3">
          <input
            type="text"
            placeholder="Filter..."
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setHighlightIdx(-1);
            }}
            className="w-full rounded-md border border-border bg-bg-secondary px-3 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-blue-500"
            aria-label="Filter table"
          />
        </div>
      )}
      <table ref={tableRef} className="w-full text-sm" role="table" tabIndex={0}>
        <thead>
          <tr role="row">
            {columns.map((col) => (
              <th
                key={col.key}
                className={clsx(
                  'px-3 py-2 text-left text-xs font-medium text-text-tertiary border-b border-border',
                  col.sortable && 'cursor-pointer select-none hover:text-text-secondary',
                )}
                onClick={() => handleHeaderClick(col)}
                aria-sort={
                  sortCol === col.key
                    ? sortAsc
                      ? 'ascending'
                      : 'descending'
                    : undefined
                }
              >
                {col.header}
                {sortCol === col.key && (
                  <span className="ml-1">{sortAsc ? '↑' : '↓'}</span>
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {filtered.map((row, idx) => {
            const key = rowKey(row);
            const isHighlighted = idx === highlightIdx;
            const isSelected = key === selectedKey;
            return (
              <tr
                key={key}
                role="row"
                aria-selected={isHighlighted || isSelected}
                className={clsx(
                  'border-b border-border transition-colors duration-150 cursor-pointer',
                  (isHighlighted || isSelected) ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/50',
                )}
                onClick={() => onSelect?.(row)}
              >
                {columns.map((col) => (
                  <td
                    key={col.key}
                    className="px-3 py-2 text-text-primary"
                    data-numeric={col.numeric || undefined}
                    style={col.numeric ? { fontVariantNumeric: 'tabular-nums' } : undefined}
                  >
                    {col.render(row)}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
      {filtered.length === 0 && search && (
        <EmptyState message="No matching results." />
      )}
    </div>
  );
}
