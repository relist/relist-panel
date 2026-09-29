import { describe, expect, it } from 'vitest';

import { volumeArchiveUrl, volumeContentUrl, volumeReadApiUrl, volumeReadUrl, volumeRestoreUrl } from './api';

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
