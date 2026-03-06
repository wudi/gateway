import { test, expect } from '@playwright/test';

test.describe('Status Page', () => {
  test('shows system summary with correct route count', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByText('Routes')).toBeVisible();
    await expect(page.getByText('3')).toBeVisible();
  });

  test('shows health indicator', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByText('Health')).toBeVisible();
  });
});
