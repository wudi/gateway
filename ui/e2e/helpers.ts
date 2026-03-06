import { type Page } from '@playwright/test';

const BASE = 'http://localhost:8081';

export async function resetMutations(): Promise<void> {
  try {
    await fetch(`${BASE}/circuit-breakers/cb-cache-route/reset`, { method: 'POST' });
    await fetch(`${BASE}/circuit-breakers/retry-rl-route/reset`, { method: 'POST' });
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
