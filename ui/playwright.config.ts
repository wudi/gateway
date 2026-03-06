import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'html',
  use: {
    baseURL: 'http://localhost:8081',
    trace: 'on-first-retry',
  },
  // globalSetup removed — webServer.url handles readiness polling
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
      testIgnore: /visual\//,
    },
    {
      name: 'visual',
      use: { ...devices['Desktop Chrome'] },
      testDir: './e2e/visual',
    },
  ],
  webServer: {
    command: '../build/runway -config e2e/fixtures/test-config.yaml',
    url: 'http://localhost:8081/routes',
    reuseExistingServer: !process.env.CI,
    timeout: 15000,
  },
});
