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

  it('sends each pull call to its route with its body', async () => {
    const calls = stubFetch();
    await api.migrationConnect('https://old.example.com', 'tok');
    await api.migrationRecheck(['/srv/up']);
    await api.migrationStart(['/srv/up']);
    await api.migrationProgress();
    await api.migrationRetry();
    expect(calls).toEqual([
      { url: '/api/migration/pull/connect', method: 'POST', body: { sourceUrl: 'https://old.example.com', token: 'tok' } },
      { url: '/api/migration/pull/recheck', method: 'POST', body: { hostPaths: ['/srv/up'] } },
      { url: '/api/migration/pull/start', method: 'POST', body: { hostPaths: ['/srv/up'] } },
      { url: '/api/migration/pull/progress', method: 'GET', body: undefined },
      { url: '/api/migration/pull/retry', method: 'POST', body: undefined },
    ]);
    const abort = stubFetch(204);
    await api.migrationAbort();
    expect(abort).toEqual([{ url: '/api/migration/pull/abort', method: 'POST', body: undefined }]);
  });

  it('carries the status of a refused call', async () => {
    stubFetch(401, { error: 'authentication required' });
    await expect(api.migrationProgress()).rejects.toEqual(new ApiError(401, 'authentication required'));
  });
});
