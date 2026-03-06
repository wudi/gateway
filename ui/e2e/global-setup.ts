import { type FullConfig } from '@playwright/test';

async function waitForReady(url: string, timeoutMs = 15000): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch {
      // not ready yet
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`Server not ready within ${timeoutMs}ms at ${url}`);
}

export default async function globalSetup(_config: FullConfig) {
  // The webServer config in playwright.config.ts handles starting the process.
  // We just need to wait for both health and routes to be ready.
  await waitForReady('http://localhost:8081/health');
  await waitForReady('http://localhost:8081/routes');
}
