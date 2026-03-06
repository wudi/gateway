import { createContext, useContext, useState, useCallback, useEffect, type ReactNode } from 'react';

interface PollingContextValue {
  interval: number | null;
  setInterval: (interval: number | null) => void;
}

const PollingContext = createContext<PollingContextValue>({
  interval: 5000,
  setInterval: () => {},
});

const STORAGE_KEY = 'runway-polling-interval';

export function PollingProvider({ children }: { children: ReactNode }) {
  const [interval, setIntervalState] = useState<number | null>(() => {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === 'off') return null;
    if (stored) {
      const parsed = parseInt(stored, 10);
      if (!isNaN(parsed)) return parsed;
    }
    return 5000;
  });

  const setInterval = useCallback((value: number | null) => {
    setIntervalState(value);
    localStorage.setItem(STORAGE_KEY, value === null ? 'off' : String(value));
  }, []);

  return (
    <PollingContext.Provider value={{ interval, setInterval }}>
      {children}
    </PollingContext.Provider>
  );
}

export function usePolling() {
  return useContext(PollingContext);
}

export function usePollingInterval(overrideMs?: number): number | false {
  const { interval } = usePolling();
  if (interval === null) return false;
  return overrideMs ?? interval;
}

export { PollingContext };
