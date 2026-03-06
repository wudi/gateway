import { test, expect } from '@playwright/test';

test.describe('Visual Regression', () => {
  test('status page healthy state', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page).toHaveScreenshot('status-healthy.png', {
      maxDiffPixelRatio: 0.001,
    });
  });

  test('routes page with detail panel', async ({ page }) => {
    await page.goto('/routes');
    await page.waitForLoadState('networkidle');
    const firstRoute = page.locator('table tbody tr').first();
    await firstRoute.click();
    await page.waitForTimeout(500);
    await expect(page).toHaveScreenshot('routes-detail.png', {
      maxDiffPixelRatio: 0.001,
    });
  });
});
