import { describe, expect, it } from 'vitest';

import type { ComposeVolume, ComposeVolumes, VolumeEntry } from '../data/types';
import {
  entryFacts,
  explorerView,
  formatMode,
  isEscapeRefusal,
  isNotDirectoryRefusal,
  isSymlink,
  joinPath,
  linkEscapes,
  openFailure,
  operationRefusal,
  parentPath,
  pathSegments,
  requiresConfirmation,
} from './volume-explorer-view';

function volume(overrides: Partial<ComposeVolume> = {}): ComposeVolume {
  return {
    kind: 'volume',
    logicalName: 'pgdata',
    source: 'demo_pgdata',
    uses: [{ service: 'api', target: '/var/lib/postgresql/data', readOnly: false }],
    preflight: { status: 'ok', warning: '' },
    ...overrides,
  };
}

function payload(overrides: Partial<ComposeVolumes> = {}): ComposeVolumes {
  return { available: true, services: [], volumes: [], ...overrides };
}

function entry(overrides: Partial<VolumeEntry> = {}): VolumeEntry {
  return {
    name: 'notes.txt',
    kind: 'file',
    size: 5,
    mode: 0o100644,
    mtime: '2026-09-29T14:46:21.816731135Z',
    uid: 1000,
    gid: 1000,
    ...overrides,
  };
}

describe('explorerView', () => {
  it('is loading while the page has neither a payload nor an error', () => {
    expect(explorerView(undefined, undefined, 'demo_pgdata')).toEqual({ status: 'loading' });
  });

  it('carries the CLI message in the not-deployed state, without the panel wrapper', () => {
    const view = explorerView(
      undefined,
      "volume resolution failed: couldn't find env file: /data/.env",
      'demo_pgdata',
    );
    expect(view).toEqual({
      status: 'not-deployed',
      message: "couldn't find env file: /data/.env",
    });
  });

  it('keeps any other resolution failure as an error carrying the API words', () => {
    expect(explorerView(undefined, 'volume pre-flight failed: docker: down', 'demo_pgdata')).toEqual({
      status: 'resolution-error',
      message: 'volume pre-flight failed: docker: down',
    });
  });

  it('is unavailable, with the API reason, when the feature cannot run', () => {
    expect(
      explorerView(payload({ available: false, reason: 'the panel must run in a container' }), undefined, 'x'),
    ).toEqual({ status: 'unavailable', reason: 'the panel must run in a container' });
  });

  it('is not-found when the URL names a source this Compose does not mount', () => {
    expect(explorerView(payload({ volumes: [volume()] }), undefined, '/srv/gone')).toEqual({
      status: 'not-found',
    });
  });

  it('is ready with the matching Volume, source compared exactly', () => {
    const bound = volume({ kind: 'bind', logicalName: '', source: '/srv/legacy-images' });
    expect(explorerView(payload({ volumes: [volume(), bound] }), undefined, '/srv/legacy-images')).toEqual({
      status: 'ready',
      volume: bound,
    });
  });

  it('is refused, carrying the API wording, when the pre-flight marks the source missing', () => {
    const missing = volume({
      kind: 'bind',
      logicalName: '',
      source: '/srv/retired',
      preflight: {
        status: 'missing',
        warning: 'bind source "/srv/retired" does not exist; refusing rather than letting Docker create it',
      },
    });
    expect(explorerView(payload({ volumes: [missing] }), undefined, '/srv/retired')).toEqual({
      status: 'refused',
      message:
        'volume /srv/retired is missing: bind source "/srv/retired" does not exist; refusing rather than letting Docker create it',
      volume: missing,
    });
  });
});

describe('operationRefusal', () => {
  it('builds the API’s 409 wording for a missing source', () => {
    const missing = volume({ preflight: { status: 'missing', warning: 'named volume "demo_pgdata" does not exist; it may have been deleted' } });
    expect(operationRefusal(missing)).toBe(
      'volume demo_pgdata is missing: named volume "demo_pgdata" does not exist; it may have been deleted',
    );
  });

  it('refuses nothing an ok or unverifiable source allows', () => {
    expect(operationRefusal(volume())).toBeUndefined();
    expect(operationRefusal(volume({ preflight: { status: 'unverifiable', warning: 'cannot check' } }))).toBeUndefined();
  });
});

describe('requiresConfirmation', () => {
  it('gates exactly the unverifiable bind, before the first read', () => {
    expect(requiresConfirmation(volume({ preflight: { status: 'unverifiable', warning: 'cannot check' } }))).toBe(true);
    expect(requiresConfirmation(volume())).toBe(false);
    expect(requiresConfirmation(volume({ preflight: { status: 'missing', warning: 'gone' } }))).toBe(false);
  });
});

describe('path math', () => {
  it('splits a relative path into its segments', () => {
    expect(pathSegments('')).toEqual([]);
    expect(pathSegments('invoices/2026-09')).toEqual(['invoices', '2026-09']);
  });

  it('walks one level up, stopping at the root', () => {
    expect(parentPath('invoices/2026-09')).toBe('invoices');
    expect(parentPath('invoices')).toBe('');
    expect(parentPath('')).toBe('');
  });

  it('joins a child onto the current directory', () => {
    expect(joinPath('', 'notes.txt')).toBe('notes.txt');
    expect(joinPath('invoices', '2026-09')).toBe('invoices/2026-09');
    expect(joinPath('invoices/', '2026-09')).toBe('invoices/2026-09');
  });
});

describe('entryFacts', () => {
  it('formats a file’s size, mode, owner and mtime', () => {
    const facts = entryFacts(entry({ size: 5 }), '');
    expect(facts).toEqual({
      name: 'notes.txt',
      kind: 'file',
      size: '5 B',
      mode: '0644',
      owner: '1000:1000',
      modified: '2026-09-29T14:46:21.816731135Z',
      target: undefined,
      escapes: false,
    });
  });

  it('shows a directory’s own size as an em dash, not as bytes', () => {
    expect(entryFacts(entry({ kind: 'directory', size: 40, mode: 0x80000000 + 0o755 }), '').size).toBe('—');
    expect(entryFacts(entry({ kind: 'directory', mode: 0x80000000 + 0o755 }), '').mode).toBe('0755');
  });

  it('carries a symlink’s target and discloses the ones that must leave the root', () => {
    const inside = entryFacts(entry({ kind: 'symlink', link: 'inside/real.txt', mode: 0x8000000 + 0o777 }), '');
    expect(inside.target).toBe('inside/real.txt');
    expect(inside.escapes).toBe(false);

    const absolute = entryFacts(entry({ kind: 'symlink', link: '/etc/passwd' }), '');
    expect(absolute.target).toBe('/etc/passwd');
    expect(absolute.escapes).toBe(true);
  });

  it('does not call a regular entry a symlink', () => {
    expect(entryFacts(entry(), '').target).toBeUndefined();
    expect(isSymlink(entry())).toBe(false);
    expect(isSymlink(entry({ kind: 'symlink', link: 'x' }))).toBe(true);
  });
});

describe('formatMode', () => {
  it('keeps the low nine bits, zero-padded, and drops the type bits', () => {
    expect(formatMode(0o100644)).toBe('0644');
    expect(formatMode(0x80000000 + 0o755)).toBe('0755');
    expect(formatMode(0x8000000 + 0o777)).toBe('0777');
    expect(formatMode(0)).toBe('0000');
  });
});

describe('linkEscapes', () => {
  it('treats an absolute target as outside, whatever it names', () => {
    expect(linkEscapes(entry({ kind: 'symlink', link: '/etc/passwd' }), '')).toBe(true);
    expect(linkEscapes(entry({ kind: 'symlink', link: '/data/inside.txt' }), '')).toBe(true);
  });

  it('normalizes a relative target against the current directory', () => {
    expect(linkEscapes(entry({ kind: 'symlink', link: 'sibling.txt' }), 'invoices')).toBe(false);
    expect(linkEscapes(entry({ kind: 'symlink', link: '../shared.txt' }), 'invoices')).toBe(false);
    expect(linkEscapes(entry({ kind: 'symlink', link: '../../secret.txt' }), 'invoices')).toBe(true);
    expect(linkEscapes(entry({ kind: 'symlink', link: '../invoices/sibling.txt' }), 'invoices')).toBe(false);
    expect(linkEscapes(entry({ kind: 'symlink', link: 'sub/../../outside' }), '')).toBe(true);
  });

  it('says nothing about non-symlinks', () => {
    expect(linkEscapes(entry(), '')).toBe(false);
    expect(linkEscapes(entry({ kind: 'symlink' }), '')).toBe(false);
  });
});

describe('openFailure', () => {
  it('discloses the target of an escaping symlink behind the API’s own refusal', () => {
    const link = entry({ kind: 'symlink', name: 'link-etc', link: '/etc/passwd' });
    const failure = openFailure(
      link,
      'volume list failed: docker: exit status 1: relist-volhelper: list "link-etc": openat link-etc: path escapes from parent',
    );
    expect(failure.message).toBe('link-etc points outside this Volume');
    expect(failure.description).toContain('/etc/passwd');
    expect(failure.description).toContain('path escapes from parent');
  });

  it('keeps any other failure in the API’s words', () => {
    expect(openFailure(entry(), 'volume list failed: docker: exit status 1: permission denied')).toEqual({
      message: 'Could not open notes.txt',
      description: 'volume list failed: docker: exit status 1: permission denied',
    });
  });

  it('recognizes exactly the escape phrase', () => {
    expect(isEscapeRefusal('openat link: path escapes from parent')).toBe(true);
    expect(isEscapeRefusal('not a directory')).toBe(false);
  });

  it('recognizes the kernel’s "not a directory" as a file link, distinct from an escape', () => {
    expect(isNotDirectoryRefusal('relist-volhelper: list "link": readdirent /data/link: not a directory')).toBe(true);
    expect(isNotDirectoryRefusal('openat link: path escapes from parent')).toBe(false);
  });
});
