import { test, expect } from '@playwright/test';

test.describe('Navigation', () => {
  test('Status page loads at /ui/', async ({ page }) => {
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');
  });

  test('click each sidebar link navigates correctly', async ({ page }) => {
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');

    const pages = [
      { name: 'Routes', heading: 'Routes' },
      { name: 'Infrastructure', heading: 'Infrastructure' },
      { name: 'Traffic Control', heading: 'Traffic Control' },
      { name: 'Deployments', heading: 'Deployments' },
      { name: 'Security', heading: 'Security' },
      { name: 'Operations', heading: 'Operations' },
      { name: 'Status', heading: 'Status' },
    ];

    for (const p of pages) {
      await page.getByRole('link', { name: p.name }).click();
      await expect(page.locator('h1')).toContainText(p.heading);
    }
  });

  test('SPA fallback serves index.html for unknown paths', async ({ page }) => {
    const response = await page.goto('/ui/nonexistent');
    expect(response?.status()).toBe(200);
    await expect(page.locator('#root')).toBeVisible();
  });

  test('Ctrl+K opens search', async ({ page }) => {
    await page.goto('/ui/');
    await expect(page.locator('h1')).toContainText('Status');
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('dialog', { name: 'Search' })).toBeVisible();
  });
});
