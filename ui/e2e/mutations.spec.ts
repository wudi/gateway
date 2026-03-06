import { test, expect } from '@playwright/test';
import { resetMutations } from './helpers';

test.describe('Mutations', () => {
  test.afterEach(async () => {
    await resetMutations();
  });

  test('config reload shows success', async ({ page }) => {
    await page.goto('/operations');
    await page.getByText('Reload Config').click();
    await expect(page.getByText('Configuration reloaded successfully')).toBeVisible();
  });
});
