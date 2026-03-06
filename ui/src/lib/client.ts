export class FetchError extends Error {
  constructor(
    public status: number,
    public body: string,
  ) {
    super(`HTTP ${status}: ${body}`);
    this.name = 'FetchError';
  }
}

export async function fetchJSON<T>(path: string, options?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {};
  if (options?.body) {
    headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(path, {
    ...options,
    headers: { ...headers, ...options?.headers },
  });
  if (!res.ok) {
    const body = await res.text();
    throw new FetchError(res.status, body);
  }
  return res.json() as Promise<T>;
}
