import { test, expect } from '@playwright/test';

test.describe('Visual Regression', () => {
  test('status page renders', async ({ page }) => {
    await page.goto('/ui/');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('h1')).toContainText('Status');
    await expect(page).toHaveScreenshot('status-page.png', {
      maxDiffPixelRatio: 0.01,
    });
  });
});
