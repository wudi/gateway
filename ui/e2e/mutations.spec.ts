import { test, expect } from '@playwright/test';
import { resetMutations } from './helpers';

test.describe('Mutations', () => {
  test.afterEach(async () => {
    await resetMutations();
  });

  test('config reload shows success', async ({ page }) => {
    await page.goto('/ui/operations');
    await expect(page.locator('h1')).toContainText('Operations');
    await page.getByRole('button', { name: 'Reload Config' }).click();
    await expect(page.getByText('Configuration reloaded successfully')).toBeVisible({ timeout: 5000 });
  });
});
