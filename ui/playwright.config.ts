import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'html',
  use: {
    baseURL: 'http://localhost:8081/ui/',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'visual',
      use: { ...devices['Desktop Chrome'] },
      testDir: './e2e/visual',
    },
  ],
  globalSetup: './e2e/global-setup.ts',
  webServer: {
    command: '../build/runway -config e2e/fixtures/test-config.yaml',
    url: 'http://localhost:8081/health',
    reuseExistingServer: !process.env.CI,
    timeout: 15000,
  },
});
