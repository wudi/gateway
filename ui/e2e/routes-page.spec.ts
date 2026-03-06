import { test, expect } from '@playwright/test';
import { waitForRoutes } from './helpers';

test.describe('Routes Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/routes');
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
    await expect(page.getByRole('complementary')).toBeVisible();
  });

  test('Esc closes detail panel', async ({ page }) => {
    await waitForRoutes(page, 3);
    await page.getByText('cb-cache-route').click();
    await expect(page.getByRole('complementary')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('complementary')).not.toBeVisible();
  });
});
