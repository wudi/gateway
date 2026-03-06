import { test, expect } from '@playwright/test';
import { waitForRoutes } from './helpers';

test.describe('Layout Stability', () => {
  test('route list renders all 3 routes consistently', async ({ page }) => {
    await page.goto('/ui/routes');
    await waitForRoutes(page, 3);

    const initialRows = await page.locator('table tbody tr').allTextContents();
    expect(initialRows).toHaveLength(3);

    const joined = initialRows.join(' ');
    expect(joined).toContain('cb-cache-route');
    expect(joined).toContain('retry-rl-route');
    expect(joined).toContain('plain-route');
  });
});
