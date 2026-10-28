import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  ApiError,
  IMAGES_URL,
  api,
  listImages,
  uploadImage,
  volumeArchiveUrl,
  volumeContentUrl,
  volumeDirectoryUrl,
  volumeFileUrl,
  volumeReadApiUrl,
  volumeReadUrl,
  volumeRestoreUrl,
  vulnCvesUrl,
  vulnFindingsUrl,
  vulnSummaryUrl,
} from './api';

/**
 * The two direct-download URLs the Explorer hands to an `<a href>`. They are
 * the one place where a resolved source and a relative path become a request,
 * so the encoding is tested here rather than through a component.
 */

const location = { projectId: 1, environmentId: 2, composeId: 3 };

describe('volumeArchiveUrl', () => {
  it('defaults to the whole Volume root — the Volumes tab’s backup', () => {
    expect(volumeArchiveUrl(location, 'demo_pgdata')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/archive?source=demo_pgdata&path=.',
    );
  });

  it('archives one directory of the Volume and encodes the path whole', () => {
    expect(volumeArchiveUrl(location, '/srv/legacy images', 'invoices/2026-09')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/archive?source=%2Fsrv%2Flegacy%20images&path=invoices%2F2026-09',
    );
  });
});

describe('volumeReadApiUrl', () => {
  it('reads one file inline — no download marker — with source and path encoded', () => {
    expect(volumeReadApiUrl(location, '/srv/legacy images', 'reports/a b\nc.txt')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/read?source=%2Fsrv%2Flegacy%20images&path=reports%2Fa%20b%0Ac.txt',
    );
  });
});

describe('volumeReadUrl', () => {
  it('marks the file an attachment and encodes source and path', () => {
    expect(volumeReadUrl(location, 'demo_pgdata', 'reports/a b\nc.txt')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/read?source=demo_pgdata&path=reports%2Fa%20b%0Ac.txt&download=1',
    );
  });
});

describe('volumeContentUrl', () => {
  it('targets the write endpoint with source and path encoded whole', () => {
    expect(volumeContentUrl(location, '/srv/legacy images', 'reports/a b\nc.txt')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/content?source=%2Fsrv%2Flegacy%20images&path=reports%2Fa%20b%0Ac.txt',
    );
  });
});

describe('volumeDirectoryUrl', () => {
  it('targets the mkdir endpoint with source and path encoded whole', () => {
    expect(volumeDirectoryUrl(location, '/srv/legacy images', 'invoices/2026-09')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/directory?source=%2Fsrv%2Flegacy%20images&path=invoices%2F2026-09',
    );
  });
});

describe('volumeFileUrl', () => {
  it('targets the create endpoint with source and path encoded whole', () => {
    expect(volumeFileUrl(location, 'demo_pgdata', 'notes/a b.txt')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/file?source=demo_pgdata&path=notes%2Fa%20b.txt',
    );
  });
});

describe('volumeRestoreUrl', () => {
  it('targets the restore endpoint with source and path encoded; the Volume root is `.`', () => {
    expect(volumeRestoreUrl(location, 'demo_pgdata')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/restore?source=demo_pgdata&path=.',
    );
    expect(volumeRestoreUrl(location, 'demo_pgdata', '')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/restore?source=demo_pgdata&path=.',
    );
  });

  it('extracts into a named directory, path encoded whole', () => {
    expect(volumeRestoreUrl(location, '/srv/legacy images', 'invoices/2026-09')).toBe(
      '/api/projects/1/environments/2/composes/3/volumes/restore?source=%2Fsrv%2Flegacy%20images&path=invoices%2F2026-09',
    );
  });
});

/**
 * Sign-in is the one endpoint where a `401` and a `429` are answers rather than
 * failures: the API tells the client to ask for a code or to wait. Every other
 * non-2xx response stays an `ApiError`, so the page can show the API's message.
 */
describe('login', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('maps a 200 response to signedIn and POSTs the credentials as JSON', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await expect(api.login({ email: 'admin@relist.local', password: 'hunter2' })).resolves.toEqual({
      kind: 'signedIn',
    });

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/auth/login');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ email: 'admin@relist.local', password: 'hunter2' });
  });

  it('maps a 401 that asks for a code to codeRequired', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async (_url: string, _init?: RequestInit) =>
          new Response(JSON.stringify({ error: 'two-factor code required', codeRequired: true }), { status: 401 }),
      ),
    );

    await expect(api.login({ email: 'admin@relist.local', password: 'hunter2' })).resolves.toEqual({
      kind: 'codeRequired',
    });
  });

  it('maps a 429 with Retry-After to rateLimited', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async (_url: string, _init?: RequestInit) =>
          new Response(JSON.stringify({ error: 'too many attempts, try again in 42 seconds' }), {
            status: 429,
            headers: { 'Retry-After': '42' },
          }),
      ),
    );

    await expect(api.login({ email: 'admin@relist.local', password: 'hunter2' })).resolves.toEqual({
      kind: 'rateLimited',
      retryAfterSeconds: 42,
    });
  });

  it('falls back to one second when a 429 has no usable Retry-After', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async (_url: string, _init?: RequestInit) =>
          new Response(JSON.stringify({ error: 'too many attempts, try again in 1 seconds' }), {
            status: 429,
            headers: { 'Retry-After': 'soon' },
          }),
      ),
    );

    await expect(api.login({ email: 'admin@relist.local', password: 'hunter2' })).resolves.toEqual({
      kind: 'rateLimited',
      retryAfterSeconds: 1,
    });
  });

  it('rejects with an ApiError carrying invalid code on a 401 that is not a code prompt', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async (_url: string, _init?: RequestInit) =>
          new Response(JSON.stringify({ error: 'invalid code' }), { status: 401 }),
      ),
    );

    await expect(api.login({ email: 'admin@relist.local', password: 'hunter2', code: '000000' })).rejects.toMatchObject({
      message: 'invalid code',
      status: 401,
    });
  });
});

describe('domain errors', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('carries the invalid field from a 400 body onto the ApiError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ error: 'x', field: 'host' }), { status: 400 })),
    );

    await expect(
      api.createDomain(location, {
        host: 'bad_host',
        path: '/',
        internalPath: '/',
        stripPath: false,
        service: 'web',
        port: 3000,
        entrypoint: '',
        https: false,
        certProvider: 'none',
        certResolver: '',
        middlewares: [],
      }),
    ).rejects.toMatchObject({ message: 'x', status: 400, field: 'host' });
  });
});

describe('deployments, sources and orphan volumes', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('requests paginated global history with encoded filters and omits unset values', async () => {
    const page = { items: [], total: 0, page: 2, pageSize: 50 };
    const fetch = vi.fn(async (_url: string) => new Response(JSON.stringify(page)));
    vi.stubGlobal('fetch', fetch);
    await expect(api.listGlobalDeployments({ page: 2, pageSize: 50, projectId: 5, status: 'failed', from: '2026-10-13T00:00:00+07:00' })).resolves.toEqual(page);
    const url = new URL(fetch.mock.calls[0][0], 'http://panel.localhost');
    expect(url.pathname).toBe('/api/deployments');
    expect(Object.fromEntries(url.searchParams)).toEqual({ page: '2', pageSize: '50', projectId: '5', status: 'failed', from: '2026-10-13T00:00:00+07:00' });
    await api.listGlobalDeployments({});
    expect(fetch.mock.calls[1][0]).toBe('/api/deployments');
  });

  it('POSTs the deployment request to deployments as JSON', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response('{"number":4}', { status: 202 }));
    vi.stubGlobal('fetch', fetch);

    await expect(
      api.enqueueDeployment(location, { mode: 'deploy', freshVolumes: true, confirmName: 'web' }),
    ).resolves.toEqual({ number: 4 });

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/projects/1/environments/2/composes/3/deployments');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ mode: 'deploy', freshVolumes: true, confirmName: 'web' });
  });

  it('accepts a cancel answered 202 with an empty body', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response(null, { status: 202 }));
    vi.stubGlobal('fetch', fetch);

    await expect(api.cancelDeployment(location, 4)).resolves.toBeUndefined();
    expect(fetch.mock.calls[0][0]).toBe('/api/projects/1/environments/2/composes/3/deployments/4/cancel');
  });

  it('uploads a zip as multipart FormData under the file field', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response('{"kind":"zip"}', { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await api.uploadSource(location, new File(['x'], 'code.zip'));

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/projects/1/environments/2/composes/3/source/upload');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBeInstanceOf(FormData);
    expect((init?.body as FormData).get('file')).toBeInstanceOf(File);
  });

  it('reads a missing Source as null', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"no source"}', { status: 404 })));
    await expect(api.getSource(location)).resolves.toBeNull();
  });

  it('derives the Dockerfile name from each detected path', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('[{"path":"api/Dockerfile.prod","service":"api","context":"api"}]', { status: 200 })),
    );
    await expect(api.detectDockerfiles(location)).resolves.toEqual([
      { path: 'api/Dockerfile.prod', service: 'api', context: 'api', dockerfile: 'Dockerfile.prod' },
    ]);
  });

  it('lists orphans from the orphans endpoint', async () => {
    const fetch = vi.fn(
      async (_url: string, _init?: RequestInit) => new Response('{"orphans":[]}', { status: 200 }),
    );
    vi.stubGlobal('fetch', fetch);

    await expect(api.listOrphanVolumes(location)).resolves.toEqual({ orphans: [] });
    expect(fetch.mock.calls[0][0]).toBe('/api/projects/1/environments/2/composes/3/volumes/orphans');
  });

  it('DELETEs one orphan with its name encoded whole', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetch);

    await api.deleteOrphanVolume(location, 'a/b');

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/projects/1/environments/2/composes/3/volumes/orphans/a%2Fb');
    expect(init?.method).toBe('DELETE');
  });
});

describe('terminalServices', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('lists the Services and their shell probes from the terminal endpoint', async () => {
    const payload = {
      available: true,
      reason: '',
      services: [
        {
          name: 'app',
          containers: [{ id: 'c1', name: 'relist-c3-app-1', number: 1, shells: { bash: true, sh: true } }],
        },
      ],
    };
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify(payload), { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await expect(api.terminalServices(location)).resolves.toEqual(payload);
    expect(fetch.mock.calls[0][0]).toBe('/api/projects/1/environments/2/composes/3/terminal/services');
  });
});

/**
 * The Account page's calls: one read of the admin account and the three
 * two-factor mutations. Each one is a thin path/method/body wrapper, so the
 * test pins the wire shape rather than the component.
 */
describe('account and two-factor', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('GETs the account', async () => {
    const fetch = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response(JSON.stringify({ email: 'admin@relist.local', twoFactorEnabled: false }), { status: 200 }),
    );
    vi.stubGlobal('fetch', fetch);

    await expect(api.account()).resolves.toEqual({ email: 'admin@relist.local', twoFactorEnabled: false });

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/auth/account');
    expect(init?.method).toBe('GET');
  });

  it('POSTs the password to setup and returns the secret and URI', async () => {
    const fetch = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response(JSON.stringify({ secret: 'JBSWY3DPEHPK3PXP', otpauthUri: 'otpauth://totp/Relist' }), { status: 200 }),
    );
    vi.stubGlobal('fetch', fetch);

    await expect(api.twoFactorSetup('hunter2')).resolves.toEqual({
      secret: 'JBSWY3DPEHPK3PXP',
      otpauthUri: 'otpauth://totp/Relist',
    });

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/auth/2fa/setup');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ password: 'hunter2' });
  });

  it('POSTs the code to enable and answers 204 with no body', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetch);

    await expect(api.twoFactorEnable('123456')).resolves.toBeUndefined();

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/auth/2fa/enable');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ code: '123456' });
  });

  it('POSTs the password and code to disable', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetch);

    await expect(api.twoFactorDisable('hunter2', '654321')).resolves.toBeUndefined();

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/auth/2fa/disable');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ password: 'hunter2', code: '654321' });
  });
});

/** The image page's calls: the host's image list and the archive upload (XHR, for progress). */
class FakeXhr {
  static last: FakeXhr;
  static created = 0;
  method = '';
  url = '';
  body: unknown = undefined;
  status = 0;
  responseText = '';
  upload: { onprogress?: (event: { loaded: number; total: number; lengthComputable: boolean }) => void } = {};
  onload?: () => void;
  onerror?: () => void;
  onabort?: () => void;
  abort = vi.fn(() => this.onabort?.());

  constructor() {
    FakeXhr.last = this;
    FakeXhr.created += 1;
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  send(body: unknown) {
    this.body = body;
  }

  respond(status: number, responseText: string) {
    this.status = status;
    this.responseText = responseText;
    this.onload?.();
  }
}

describe('uploadImage', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('PUTs the file to /api/images and resolves with the loaded names', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXhr);
    const file = new Blob(['tar']);

    const result = uploadImage(file, () => {});
    FakeXhr.last.respond(200, '{"images":["web:1"]}');

    await expect(result).resolves.toEqual(['web:1']);
    expect(FakeXhr.last.method).toBe('PUT');
    expect(FakeXhr.last.url).toBe(IMAGES_URL);
    expect(IMAGES_URL).toBe('/api/images');
    expect(FakeXhr.last.body).toBe(file);
  });

  it('reports progress', () => {
    vi.stubGlobal('XMLHttpRequest', FakeXhr);
    const onProgress = vi.fn();

    void uploadImage(new Blob(['tar']), onProgress);
    FakeXhr.last.upload.onprogress?.({ loaded: 5, total: 10, lengthComputable: true });

    expect(onProgress).toHaveBeenCalledWith(5, 10);
  });

  it("rejects with the server's error message", async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXhr);

    const result = uploadImage(new Blob(['tar']), () => {});
    FakeXhr.last.respond(422, '{"error":"invalid tar header"}');

    await expect(result).rejects.toBeInstanceOf(ApiError);
    await expect(result).rejects.toMatchObject({ status: 422, message: 'invalid tar header' });
  });

  it('falls back to a status message when the error body is not JSON', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXhr);

    const result = uploadImage(new Blob(['tar']), () => {});
    FakeXhr.last.respond(504, '<html>gateway</html>');

    await expect(result).rejects.toMatchObject({ status: 504, message: 'Request failed (504)' });
  });

  it('rejects a dropped connection', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXhr);

    const result = uploadImage(new Blob(['tar']), () => {});
    FakeXhr.last.onerror?.();

    await expect(result).rejects.toMatchObject({ status: 0, message: 'Upload failed — connection lost' });
  });

  it('rejects when cancelled', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXhr);
    const controller = new AbortController();

    const result = uploadImage(new Blob(['tar']), () => {}, controller.signal);
    controller.abort();

    expect(FakeXhr.last.abort).toHaveBeenCalled();
    await expect(result).rejects.toMatchObject({ status: 0, message: 'Upload cancelled' });
  });

  it('rejects at once, without sending, when the signal is already aborted', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXhr);
    FakeXhr.created = 0;

    const result = uploadImage(new Blob(['tar']), () => {}, AbortSignal.abort());

    await expect(result).rejects.toMatchObject({ status: 0, message: 'Upload cancelled' });
    expect(FakeXhr.created).toBe(0);
  });
});

describe('listImages', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('unwraps the list', async () => {
    const images = [{ id: 'sha256:a', tags: ['web:1'], size: 10, created: '2026-01-01T00:00:00Z', inUse: false }];
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ images }), { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await expect(listImages()).resolves.toEqual(images);
    expect(fetch.mock.calls[0][0]).toBe('/api/images');
  });
});

describe('createSshKey', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('POSTs the name as JSON to /api/ssh-keys', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response('{}', { status: 201 }));
    vi.stubGlobal('fetch', fetch);

    await api.createSshKey('ci');

    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/ssh-keys');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe('{"name":"ci"}');
  });
});

describe('vulnerability URLs', () => {
  it('builds the CVE query with only the filters set', () => {
    expect(vulnCvesUrl({ composeId: 9, severity: ['CRITICAL', 'HIGH'], fixable: true, new: false, q: 'open ssl', page: 2, pageSize: 20 })).toBe(
      '/api/vulnerabilities/cves?composeId=9&severity=CRITICAL%2CHIGH&fixable=1&q=open+ssl&page=2&pageSize=20',
    );
  });

  it('scopes the summary to a Project, a Compose or the host', () => {
    expect(vulnSummaryUrl({})).toBe('/api/vulnerabilities/summary');
    expect(vulnSummaryUrl({ projectId: 4 })).toBe('/api/vulnerabilities/summary?projectId=4');
    expect(vulnSummaryUrl({ composeId: 7 })).toBe('/api/vulnerabilities/summary?composeId=7');
  });

  it('encodes an image id in the findings path', () => {
    expect(vulnFindingsUrl('sha256:abc')).toBe('/api/vulnerabilities/images/sha256%3Aabc/findings');
  });
});
