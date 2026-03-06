import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { PollingProvider, usePolling } from './PollingContext';
import type { ReactNode } from 'react';

function wrapper({ children }: { children: ReactNode }) {
  return <PollingProvider>{children}</PollingProvider>;
}

describe('PollingContext', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('default interval is 5000ms', () => {
    const { result } = renderHook(() => usePolling(), { wrapper });
    expect(result.current.interval).toBe(5000);
  });

  it('setter updates value', () => {
    const { result } = renderHook(() => usePolling(), { wrapper });
    act(() => {
      result.current.setInterval(10000);
    });
    expect(result.current.interval).toBe(10000);
  });

  it('"off" disables polling', () => {
    const { result } = renderHook(() => usePolling(), { wrapper });
    act(() => {
      result.current.setInterval(null);
    });
    expect(result.current.interval).toBeNull();
  });

  it('persists to localStorage', () => {
    const { result } = renderHook(() => usePolling(), { wrapper });
    act(() => {
      result.current.setInterval(30000);
    });
    expect(localStorage.getItem('runway-polling-interval')).toBe('30000');
  });

  it('persists "off" to localStorage', () => {
    const { result } = renderHook(() => usePolling(), { wrapper });
    act(() => {
      result.current.setInterval(null);
    });
    expect(localStorage.getItem('runway-polling-interval')).toBe('off');
  });
});
