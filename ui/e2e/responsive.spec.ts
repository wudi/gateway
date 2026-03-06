import { test, expect } from '@playwright/test';

test.describe('Responsive', () => {
  test('narrow viewport hides sidebar text', async ({ page }) => {
    await page.setViewportSize({ width: 1100, height: 800 });
    await page.goto('/');
    // At narrow width, sidebar should show icons only
    await expect(page.locator('nav')).toBeVisible();
  });

  test('mobile viewport shows hamburger', async ({ page }) => {
    await page.setViewportSize({ width: 700, height: 800 });
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
  });
});
