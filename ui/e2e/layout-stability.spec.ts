import { test, expect } from '@playwright/test';
import { waitForRoutes } from './helpers';

test.describe('Layout Stability', () => {
  test('row order unchanged after poll refresh', async ({ page }) => {
    await page.goto('/routes');
    await waitForRoutes(page, 3);

    // Capture initial order
    const initialRows = await page.locator('table tbody tr').allTextContents();

    // Wait for a poll cycle (5s default)
    await page.waitForTimeout(6000);

    // Capture after refresh
    const afterRows = await page.locator('table tbody tr').allTextContents();
    expect(afterRows).toEqual(initialRows);
  });
});
