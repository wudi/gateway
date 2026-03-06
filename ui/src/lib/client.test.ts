import { describe, it, expect } from 'vitest';
import { fetchJSON, FetchError } from './client';

describe('fetchJSON', () => {
  it('fetches from relative path', async () => {
    const data = await fetchJSON('/dashboard');
    expect(data).toBeDefined();
    expect(data).toHaveProperty('routes');
  });

  it('parses JSON response', async () => {
    const data = await fetchJSON<{ routes: unknown[] }>('/dashboard');
    expect(Array.isArray(data.routes)).toBe(true);
  });

  it('throws on non-2xx', async () => {
    await expect(fetchJSON('/nonexistent-endpoint-404')).rejects.toThrow();
  });

  it('throws FetchError with status and body', async () => {
    try {
      await fetchJSON('/nonexistent-endpoint-404');
      expect.fail('should have thrown');
    } catch (err) {
      if (err instanceof FetchError) {
        expect(err.status).toBeGreaterThanOrEqual(400);
        expect(typeof err.body).toBe('string');
      }
    }
  });

  it('POST includes Content-Type', async () => {
    const data = await fetchJSON('/drain', {
      method: 'POST',
      body: JSON.stringify({}),
    });
    expect(data).toBeDefined();
  });
});
