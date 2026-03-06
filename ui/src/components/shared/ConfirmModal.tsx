import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Spinner } from './Spinner';
import { X } from 'lucide-react';

interface ConfirmModalProps {
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
  isPending?: boolean;
  error?: string;
  requireTypedConfirmation?: string;
}

export function ConfirmModal({
  title,
  description,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  onConfirm,
  onCancel,
  isPending = false,
  error,
  requireTypedConfirmation,
}: ConfirmModalProps) {
  const [typed, setTyped] = useState('');
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousActiveRef = useRef<Element | null>(null);

  const confirmEnabled = requireTypedConfirmation
    ? typed === requireTypedConfirmation
    : true;

  useEffect(() => {
    previousActiveRef.current = document.activeElement;
    const firstFocusable = dialogRef.current?.querySelector<HTMLElement>(
      'input, button, [tabindex]:not([tabindex="-1"])',
    );
    firstFocusable?.focus();

    return () => {
      if (previousActiveRef.current instanceof HTMLElement) {
        previousActiveRef.current.focus();
      }
    };
  }, []);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onCancel();
        return;
      }
      if (e.key === 'Tab') {
        const focusable = dialogRef.current?.querySelectorAll<HTMLElement>(
          'input, button, [tabindex]:not([tabindex="-1"])',
        );
        if (!focusable || focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [onCancel]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60" onClick={onCancel} />
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="relative z-10 w-full max-w-md rounded-lg border border-border backdrop-blur-xl bg-white/5 p-6"
      >
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold text-text-primary">{title}</h2>
          <button
            type="button"
            onClick={onCancel}
            className="text-text-tertiary hover:text-text-primary transition-colors duration-150"
            aria-label="Close"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {description && (
          <div className="mb-4 text-sm text-text-secondary">{description}</div>
        )}

        {requireTypedConfirmation && (
          <div className="mb-4">
            <label className="block text-sm text-text-secondary mb-1">
              Type <span className="font-mono text-text-primary">{requireTypedConfirmation}</span> to confirm
            </label>
            <input
              type="text"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              className="w-full rounded-md border border-border bg-bg-secondary px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-blue-500"
              aria-label="Confirmation input"
            />
          </div>
        )}

        {error && (
          <div className="mb-4 rounded-md bg-red-500/10 px-3 py-2 text-sm text-red-400" role="alert">
            {error}
          </div>
        )}

        <div className="flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            className="px-4 py-2 rounded-md text-sm text-text-secondary hover:text-text-primary transition-colors duration-150"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={!confirmEnabled || isPending}
            aria-disabled={!confirmEnabled || isPending}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors duration-150 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {isPending && <Spinner size="sm" />}
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
