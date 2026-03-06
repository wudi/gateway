import { test, expect } from '@playwright/test';
import { waitForRoutes } from './helpers';

test.describe('Routes Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/ui/routes');
    await expect(page.locator('h1')).toContainText('Routes');
  });

  test('all 3 test routes listed', async ({ page }) => {
    await waitForRoutes(page, 3);
    await expect(page.getByText('cb-cache-route')).toBeVisible();
    await expect(page.getByText('retry-rl-route')).toBeVisible();
    await expect(page.getByText('plain-route')).toBeVisible();
  });

  test('click route opens detail panel', async ({ page }) => {
    await waitForRoutes(page, 3);
    await page.getByText('cb-cache-route').click();
    const panel = page.getByRole('complementary', { name: /Details for/ });
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('cb-cache-route');
  });

  test('detail panel shows path and backends', async ({ page }) => {
    await waitForRoutes(page, 3);
    await page.getByText('cb-cache-route').click();
    const panel = page.getByRole('complementary', { name: /Details for/ });
    await expect(panel.getByRole('heading', { name: 'Path' })).toBeVisible();
    await expect(panel.getByText('/api/*path')).toBeVisible();
    await expect(panel.getByRole('heading', { name: 'Backends' })).toBeVisible();
  });

  test('detail panel shows circuit breaker for cb-cache-route', async ({ page }) => {
    await waitForRoutes(page, 3);
    await page.getByText('cb-cache-route').click();
    const panel = page.getByRole('complementary', { name: /Details for/ });
    await expect(panel.getByText('Circuit Breaker')).toBeVisible();
    await expect(panel.getByText('closed')).toBeVisible();
  });

  test('Esc closes detail panel', async ({ page }) => {
    await waitForRoutes(page, 3);
    await page.getByText('cb-cache-route').click();
    const panel = page.getByRole('complementary', { name: /Details for/ });
    await expect(panel).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(panel).not.toBeVisible();
  });
});
