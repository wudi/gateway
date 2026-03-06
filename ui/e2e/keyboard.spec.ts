import { test, expect } from '@playwright/test';

test.describe('Keyboard Navigation', () => {
  test('Ctrl+K opens global search from any page', async ({ page }) => {
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('dialog', { name: 'Search' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).not.toBeVisible();
  });

  test('Ctrl+K from operations page', async ({ page }) => {
    await page.goto('/ui/operations');
    await expect(page.locator('h1')).toContainText('Operations');
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('dialog', { name: 'Search' })).toBeVisible();
  });
});
