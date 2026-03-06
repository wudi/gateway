import { test, expect } from '@playwright/test';

test.describe('Status Page', () => {
  test('shows system summary', async ({ page }) => {
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');
    // StatRow items
    await expect(page.getByText('Uptime')).toBeVisible();
    await expect(page.getByText('Listeners')).toBeVisible();
    // The main content area should have route count
    await expect(page.locator('main')).toContainText('Routes');
  });

  test('shows health indicator', async ({ page }) => {
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');
    await expect(page.getByRole('heading', { name: 'Health' })).toBeVisible();
    // Health status badge shows status text
    await expect(page.locator('[data-status]')).toBeVisible();
  });

  test('shows route count from dashboard', async ({ page }) => {
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');
    // 3 routes in the dashboard
    await expect(page.getByText('3', { exact: true })).toBeVisible();
  });
});
