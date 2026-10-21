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
  it('sends each old-host call to its route', async () => {
    const calls = stubFetch();
    await api.migrationStatus();
    await api.createMigrationToken('24h');
    stubFetch(204);
    await api.revokeMigrationToken();
    await api.resumeMigration('RESUME');
    expect(calls).toEqual([
      { url: '/api/migration/status', method: 'GET', body: undefined },
      { url: '/api/migration/tokens', method: 'POST', body: { lifetime: '24h' } },
    ]);
  });

  it('carries the status of a refused call', async () => {
    stubFetch(401, { error: 'authentication required' });
    await expect(api.migrationStatus()).rejects.toEqual(new ApiError(401, 'authentication required'));
  });
});
