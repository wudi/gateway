import { useState, useEffect, useRef, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { Search } from 'lucide-react';

interface SearchResult {
  label: string;
  path: string;
  type: 'page' | 'route';
}

const pages: SearchResult[] = [
  { label: 'Status', path: '/', type: 'page' },
  { label: 'Routes', path: '/routes', type: 'page' },
  { label: 'Infrastructure', path: '/infrastructure', type: 'page' },
  { label: 'Traffic Control', path: '/traffic', type: 'page' },
  { label: 'Deployments', path: '/deployments', type: 'page' },
  { label: 'Security', path: '/security', type: 'page' },
  { label: 'Operations', path: '/operations', type: 'page' },
];

interface GlobalSearchProps {
  routeIds?: string[];
}

export function GlobalSearch({ routeIds = [] }: GlobalSearchProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [highlightIdx, setHighlightIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();

  const allResults: SearchResult[] = [
    ...pages,
    ...routeIds.map((id) => ({ label: id, path: `/routes?route=${id}`, type: 'route' as const })),
  ];

  const filtered = query
    ? allResults.filter((r) => r.label.toLowerCase().includes(query.toLowerCase()))
    : allResults;

  const openSearch = useCallback(() => {
    setOpen(true);
    setQuery('');
    setHighlightIdx(0);
  }, []);

  const closeSearch = useCallback(() => {
    setOpen(false);
    setQuery('');
  }, []);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        openSearch();
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [openSearch]);

  useEffect(() => {
    if (open) {
      inputRef.current?.focus();
    }
  }, [open]);

  useEffect(() => {
    setHighlightIdx(0);
  }, [query]);

  if (!open) return null;

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      closeSearch();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      setHighlightIdx((prev) => Math.min(prev + 1, filtered.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlightIdx((prev) => Math.max(prev - 1, 0));
    } else if (e.key === 'Enter' && filtered[highlightIdx]) {
      navigate(filtered[highlightIdx].path);
      closeSearch();
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]">
      <div className="fixed inset-0 bg-black/60" onClick={closeSearch} />
      <div
        role="dialog"
        aria-label="Search"
        className="relative z-10 w-full max-w-lg rounded-lg border border-border backdrop-blur-xl bg-white/5 overflow-hidden"
        onKeyDown={handleKeyDown}
      >
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <Search className="h-4 w-4 text-text-tertiary" />
          <input
            ref={inputRef}
            type="text"
            placeholder="Search pages and routes..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="flex-1 bg-transparent text-sm text-text-primary focus:outline-none"
            aria-label="Search input"
          />
        </div>
        <div className="max-h-64 overflow-y-auto">
          {filtered.length === 0 ? (
            <div className="px-4 py-6 text-center text-sm text-text-secondary">
              No results
            </div>
          ) : (
            <ul role="listbox">
              {filtered.map((result, idx) => (
                <li
                  key={result.path}
                  role="option"
                  aria-selected={idx === highlightIdx}
                  className={`px-4 py-2 text-sm cursor-pointer transition-colors duration-150 ${
                    idx === highlightIdx ? 'bg-bg-elevated text-text-primary' : 'text-text-secondary hover:bg-bg-elevated/50'
                  }`}
                  onClick={() => {
                    navigate(result.path);
                    closeSearch();
                  }}
                >
                  <span className="mr-2 text-xs text-text-tertiary">{result.type}</span>
                  {result.label}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
