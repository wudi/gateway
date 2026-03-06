import { test, expect } from '@playwright/test';

test.describe('Responsive', () => {
  test('default viewport shows sidebar with text labels', async ({ page }) => {
    await page.goto('/ui/');
    const nav = page.getByRole('navigation', { name: 'Main navigation' });
    await expect(nav).toBeVisible();
    await expect(nav.getByText('Status')).toBeVisible();
    await expect(nav.getByText('Routes')).toBeVisible();
  });

  test('narrow viewport still renders page', async ({ page }) => {
    await page.setViewportSize({ width: 700, height: 800 });
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');
  });
});
