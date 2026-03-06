import { useState, useEffect } from 'react';
import { Spinner } from './Spinner';
import { Check } from 'lucide-react';
import { clsx } from 'clsx';

interface ActionButtonProps {
  label: string;
  onClick: () => Promise<void> | void;
  variant?: 'default' | 'danger';
  disabled?: boolean;
  className?: string;
}

type ButtonState = 'idle' | 'loading' | 'success' | 'error';

export function ActionButton({
  label,
  onClick,
  variant = 'default',
  disabled = false,
  className,
}: ActionButtonProps) {
  const [state, setState] = useState<ButtonState>('idle');
  const [errorMessage, setErrorMessage] = useState('');

  useEffect(() => {
    if (state === 'success') {
      const timer = setTimeout(() => setState('idle'), 2000);
      return () => clearTimeout(timer);
    }
  }, [state]);

  const handleClick = async () => {
    if (state === 'loading' || disabled) return;
    setState('loading');
    setErrorMessage('');
    try {
      await onClick();
      setState('success');
    } catch (err) {
      setState('error');
      setErrorMessage(err instanceof Error ? err.message : 'Action failed');
    }
  };

  const isDisabled = disabled || state === 'loading';

  return (
    <div className="inline-flex flex-col items-start">
      <button
        type="button"
        onClick={handleClick}
        disabled={isDisabled}
        aria-disabled={isDisabled}
        className={clsx(
          'inline-flex items-center gap-2 px-3 py-1.5 rounded-md text-sm font-medium transition-colors duration-150',
          variant === 'danger'
            ? 'bg-red-500/10 text-red-400 hover:bg-red-500/20'
            : 'bg-bg-elevated text-text-primary hover:bg-white/10',
          isDisabled && 'opacity-50 cursor-not-allowed',
          className,
        )}
      >
        {state === 'loading' && <Spinner size="sm" />}
        {state === 'success' && <Check className="h-4 w-4 text-green-400" />}
        {label}
      </button>
      {state === 'error' && errorMessage && (
        <span className="mt-1 text-xs text-red-400">{errorMessage}</span>
      )}
    </div>
  );
}
