import { test, expect } from '@playwright/test';

test.describe('Keyboard Navigation', () => {
  test('Cmd+K opens global search from any page', async ({ page }) => {
    await page.goto('/');
    await page.keyboard.press('Meta+k');
    await expect(page.getByRole('dialog', { name: 'Search' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).not.toBeVisible();
  });
});
