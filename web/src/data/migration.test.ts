import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, api } from './api';

type Call = { url: string; method: string; body: unknown };

function stubFetch(status = 200, body: unknown = {}) {
  const calls: Call[] = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? 'GET', body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return new Response(status === 204 ? null : JSON.stringify(body), { status });
  }));
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('migration api', () => {
  it('reads the lock status from its route', async () => {
    const calls = stubFetch();
    await api.migrationStatus();
    expect(calls).toEqual([{ url: '/api/migration/status', method: 'GET', body: undefined }]);
  });

  it('carries the status of a refused call', async () => {
    stubFetch(401, { error: 'authentication required' });
    await expect(api.migrationStatus()).rejects.toEqual(new ApiError(401, 'authentication required'));
  });
});
