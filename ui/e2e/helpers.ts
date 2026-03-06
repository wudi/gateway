import { type Page, expect } from '@playwright/test';

const BASE = 'http://localhost:8081';

export async function resetMutations(): Promise<void> {
  try {
    // Reset circuit breakers
    await fetch(`${BASE}/circuit-breakers/cb-cache-route/reset`, { method: 'POST' });
    await fetch(`${BASE}/circuit-breakers/retry-rl-route/reset`, { method: 'POST' });
    // Cancel drain if active
    const drainRes = await fetch(`${BASE}/drain`);
    const drain = await drainRes.json();
    if (drain.draining) {
      await fetch(`${BASE}/drain`, { method: 'POST' });
    }
  } catch {
    // best-effort cleanup
  }
}

export async function waitForRoutes(page: Page, count: number): Promise<void> {
  await page.waitForFunction(
    (expected) => {
      const rows = document.querySelectorAll('table tbody tr');
      return rows.length >= expected;
    },
    count,
    { timeout: 10000 },
  );
}
