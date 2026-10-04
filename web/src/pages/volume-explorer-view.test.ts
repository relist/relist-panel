import { describe, expect, it } from 'vitest';

import { ApiError } from '../data/api';
import type { ComposeStatus, ComposeVolume, ComposeVolumes, VolumeEntry } from '../data/types';
import {
  deleteFailure,
  EDITOR_MAX_BYTES,
  EDITOR_SNIFF_BYTES,
  editFailure,
  editorContent,
  editorGate,
  editorSaveStatus,
  entryFacts,
  explorerView,
  formatMode,
  isDirty,
  isEscapeRefusal,
  isExistsRefusal,
  isNotDirectoryRefusal,
  isSymlink,
  joinPath,
  linkEscapes,
  newEntryFailure,
  newEntryNameError,
  openFailure,
  operationRefusal,
  parentPath,
  pathSegments,
  requiresConfirmation,
  RESTORE_MAX_COMPRESSED_BYTES,
  RESTORE_MAX_ENTRIES,
  RESTORE_MAX_UNCOMPRESSED_BYTES,
  restoreDisclosure,
  restoreFailure,
  restoreIntent,
  restoreTarget,
  restoreTooLarge,
  runningWriteWarning,
  uploadFailure,
  uploadTooLarge,
  UPLOAD_MAX_BYTES,
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

describe('uploadTooLarge', () => {
  it('lets a file that exactly fills the cap through, and refuses one byte more', () => {
    expect(uploadTooLarge({ name: 'at-cap.bin', size: UPLOAD_MAX_BYTES })).toBeUndefined();
    const refused = uploadTooLarge({ name: 'over-cap.bin', size: UPLOAD_MAX_BYTES + 1 });
    expect(refused?.message).toBe('Upload exceeds the 1 GiB cap');
    expect(refused?.description).toContain('over-cap.bin');
    expect(refused?.description).toContain('1 GiB');
  });
});

describe('uploadFailure / deleteFailure', () => {
  it('names the 1 GiB cap the API refused the upload for', () => {
    const failure = uploadFailure(new ApiError(413, 'upload exceeds the 1 GiB cap'));
    expect(failure.message).toBe('Upload exceeds the 1 GiB cap');
    expect(failure.description).toContain('1 GiB');
    expect(failure.description).toContain('upload exceeds the 1 GiB cap');
  });

  it('names the Volume’s free space the helper refused the write for', () => {
    const refusal =
      'volume write refused: relist-volhelper: write "backup.tar" needs 2147483648 bytes but the Volume has only 1048576 free';
    const failure = uploadFailure(new ApiError(507, refusal));
    expect(failure.message).toBe('The Volume is out of space');
    expect(failure.description).toContain(refusal);
    expect(failure.description).toContain('partial file');
  });

  it('keeps the API’s 400 wording for a request the shape rules refused', () => {
    const failure = deleteFailure(new ApiError(400, 'path must name a file or directory, not the volume root'));
    expect(failure.message).toBe('The delete was refused');
    expect(failure.description).toBe('path must name a file or directory, not the volume root');
  });

  it('explains the 409 missing source instead of leaving a bare conflict', () => {
    const failure = uploadFailure(
      new ApiError(409, 'volume demo_pgdata is missing: named volume "demo_pgdata" does not exist'),
    );
    expect(failure.message).toBe('This Volume’s source is missing');
    expect(failure.description).toContain('does not exist');
    expect(failure.description).toContain('mounting it is what would create it');
  });

  it('maps a 422 to the helper’s refusal, keeping the API’s words', () => {
    const failure = uploadFailure(new ApiError(422, 'volume extract refused: archive entry "../etc/passwd" escapes'));
    expect(failure.message).toBe('The Volume refused the file');
    expect(failure.description).toContain('escapes');
  });

  it('falls back to the API’s own message for an unmapped failure', () => {
    const failure = deleteFailure(new ApiError(502, 'volume delete failed: docker: exit status 1: permission denied'));
    expect(failure.message).toBe('Delete failed');
    expect(failure.description).toBe('volume delete failed: docker: exit status 1: permission denied');
  });

  it('says something for a rejection that is not an Error', () => {
    expect(deleteFailure('boom').description).toBe('boom');
  });
});

describe('runningWriteWarning', () => {
  const status = (running: number, total = 3): ComposeStatus => ({ running, total });

  it('is silent while no Service in the Compose is running', () => {
    expect(runningWriteWarning(status(0), volume())).toBeUndefined();
    expect(runningWriteWarning(undefined, volume())).toBeUndefined();
  });

  it('raises on a partly running Compose too — any Service up is enough', () => {
    expect(runningWriteWarning(status(1, 3), volume())).toBeDefined();
  });

  it('names the Services that mount this Volume once each, in declaration order', () => {
    const shared = volume({
      uses: [
        { service: 'api', target: '/app/uploads', readOnly: false },
        { service: 'worker', target: '/work', readOnly: true },
        { service: 'api', target: '/app/incoming', readOnly: false },
      ],
    });
    const warning = runningWriteWarning(status(1, 3), shared);
    expect(warning?.services).toEqual(['api', 'worker']);
    expect(warning?.message).toBe('This Compose is running — writing can corrupt api, worker');
  });

  it('names the consequence rather than telling the operator to be careful', () => {
    const warning = runningWriteWarning(status(2), volume());
    expect(warning?.description).toContain('database');
    expect(warning?.description).toContain('no confirmation gate');
    expect(warning?.description).not.toMatch(/be careful/i);
  });

  it('still warns without a named Service when the Volume declares no use', () => {
    const warning = runningWriteWarning(status(1), volume({ uses: [] }));
    expect(warning?.services).toEqual([]);
    expect(warning?.message).toContain('a live Service');
  });
});

describe('editorGate', () => {
  it('opens a file that exactly fills the 1 MB cap, and refuses one byte more', () => {
    expect(editorGate(entry({ size: EDITOR_MAX_BYTES }), 'notes.txt')).toBeUndefined();

    const refused = editorGate(entry({ size: EDITOR_MAX_BYTES + 1 }), 'invoices/export.csv');
    expect(refused?.message).toBe('Too large to edit');
    expect(refused?.description).toContain('invoices/export.csv');
    expect(refused?.description).toContain('1 MB');
    expect(refused?.description).toContain('not opened');
  });
});

describe('editorContent', () => {
  const text = (value: string) => new TextEncoder().encode(value);

  it('decodes valid UTF-8 text unchanged', () => {
    expect(editorContent('notes.txt', text('héllo\nworld\n'))).toEqual({
      status: 'editable',
      text: 'héllo\nworld\n',
    });
  });

  it('treats an empty file as editable text', () => {
    expect(editorContent('empty.txt', new Uint8Array())).toEqual({ status: 'editable', text: '' });
  });

  it('refuses a NUL byte anywhere in the first 8 KiB', () => {
    const bytes = new Uint8Array(EDITOR_SNIFF_BYTES).fill(0x61);
    bytes[EDITOR_SNIFF_BYTES - 1] = 0;
    const refused = editorContent('dump.bin', bytes);
    expect(refused.status).toBe('refused');
    if (refused.status === 'refused') {
      expect(refused.failure.message).toBe('This file is not text');
      expect(refused.failure.description).toContain('dump.bin');
      expect(refused.failure.description).toContain('NUL');
    }
  });

  it('sniffs exactly the first 8 KiB — a NUL past the window is not the sniff’s business', () => {
    const bytes = new Uint8Array(EDITOR_SNIFF_BYTES + 1).fill(0x61);
    bytes[EDITOR_SNIFF_BYTES] = 0;
    expect(editorContent('late-nul.bin', bytes).status).toBe('editable');
  });

  it('refuses bytes that are not valid UTF-8, so a save cannot write replacements back', () => {
    const refused = editorContent('latin1.conf', new Uint8Array([0xff, 0xfe, 0x61]));
    expect(refused.status).toBe('refused');
    if (refused.status === 'refused') {
      expect(refused.failure.message).toBe('This file is not UTF-8 text');
      expect(refused.failure.description).toContain('latin1.conf');
    }
  });
});

describe('isDirty', () => {
  it('is a strict comparison of the draft against the last saved state', () => {
    expect(isDirty('a\n', 'a\n')).toBe(false);
    expect(isDirty('a\n', 'a\n ')).toBe(true);
    expect(isDirty('', 'x')).toBe(true);
    expect(isDirty('x', '')).toBe(true);
  });
});

describe('editorSaveStatus', () => {
  const clean = { dirty: false, failure: undefined, saving: false };

  it('is clean only when nothing changed and nothing failed', () => {
    expect(editorSaveStatus(clean)).toBe('clean');
  });

  it('is dirty while the draft differs from the file', () => {
    expect(editorSaveStatus({ ...clean, dirty: true })).toBe('dirty');
  });

  it('reports the failure while the draft is still unsaved', () => {
    expect(editorSaveStatus({ ...clean, dirty: true, failure: { message: 'Save failed', description: 'x' } })).toBe(
      'failed',
    );
  });

  it('lets a save in flight outrank both dirty and a previous failure', () => {
    expect(
      editorSaveStatus({ dirty: true, failure: { message: 'Save failed', description: 'x' }, saving: true }),
    ).toBe('saving');
  });
});

describe('editFailure', () => {
  it('names the 1 GiB write cap the API refused the save for', () => {
    const failure = editFailure(new ApiError(413, 'upload exceeds the 1 GiB cap'));
    expect(failure.message).toBe('The save exceeds the 1 GiB cap');
    expect(failure.description).toContain('1 GiB');
  });

  it('names the Volume’s free space and the partial-file clean-up', () => {
    const failure = editFailure(new ApiError(507, 'write refused: needs 2097152 bytes but the Volume has 1024 free'));
    expect(failure.message).toBe('The Volume is out of space');
    expect(failure.description).toContain('partial file');
    expect(failure.description).toContain('needs 2097152 bytes');
  });

  it('keeps the API’s 400 wording for the root-path refusal', () => {
    const failure = editFailure(new ApiError(400, 'path must name a file or directory, not the volume root'));
    expect(failure.message).toBe('The save was refused');
    expect(failure.description).toBe('path must name a file or directory, not the volume root');
  });

  it('explains the 409 missing source instead of leaving a bare conflict', () => {
    const failure = editFailure(new ApiError(409, 'volume demo_pgdata is missing: named volume "demo_pgdata" does not exist'));
    expect(failure.message).toBe('This Volume’s source is missing');
    expect(failure.description).toContain('does not exist');
  });

  it('maps a 422 to the helper’s refusal and a 502 to the API’s own words', () => {
    expect(editFailure(new ApiError(422, 'volume write refused: not text')).message).toBe('The Volume refused the file');
    const failure = editFailure(new ApiError(502, 'volume write failed: docker: exit status 1: permission denied'));
    expect(failure.message).toBe('Save failed');
    expect(failure.description).toBe('volume write failed: docker: exit status 1: permission denied');
  });
});

describe('restoreTarget', () => {
  it('is `.` at the Volume root and the relative path everywhere else', () => {
    expect(restoreTarget('')).toBe('.');
    expect(restoreTarget('invoices')).toBe('invoices');
    expect(restoreTarget('invoices/2026-09')).toBe('invoices/2026-09');
  });
});

describe('restoreTooLarge', () => {
  it('lets an archive that exactly fills the compressed cap through, and refuses one byte more', () => {
    expect(restoreTooLarge({ name: 'at-cap.tar.gz', size: RESTORE_MAX_COMPRESSED_BYTES })).toBeUndefined();

    const refused = restoreTooLarge({ name: 'over-cap.tgz', size: RESTORE_MAX_COMPRESSED_BYTES + 1 });
    expect(refused?.message).toBe('The archive exceeds the 1 GiB compressed cap');
    expect(refused?.description).toContain('over-cap.tgz');
    expect(refused?.description).toContain('1 GiB');
    expect(refused?.description).toContain('never sent');
  });
});

describe('restoreIntent', () => {
  it('confirms an archive within the cap, carrying the disclosure that names the target', () => {
    const intent = restoreIntent({ name: 'backup.tar.gz', size: 1024 }, 'invoices/2026-09');
    expect(intent.status).toBe('confirm');
    if (intent.status === 'confirm') {
      expect(intent.disclosure.target).toBe('invoices/2026-09');
      expect(intent.disclosure.title).toBe('Extract backup.tar.gz?');
    }
  });

  it('refuses one byte past the cap without opening a confirmation', () => {
    const intent = restoreIntent({ name: 'huge.tgz', size: RESTORE_MAX_COMPRESSED_BYTES + 1 }, '');
    expect(intent).toEqual({
      status: 'refused',
      failure: {
        message: 'The archive exceeds the 1 GiB compressed cap',
        description: expect.stringContaining('huge.tgz'),
      },
    });
  });
});

describe('restoreDisclosure', () => {
  it('names the Volume root as `.`', () => {
    const disclosure = restoreDisclosure({ name: 'backup.tar.gz' }, '');
    expect(disclosure.target).toBe('.');
    expect(disclosure.points[0]).toContain('the current directory, .');
  });

  it('names a nested target by its relative path', () => {
    const disclosure = restoreDisclosure({ name: 'backup.tar.gz' }, 'invoices/2026-09');
    expect(disclosure.target).toBe('invoices/2026-09');
    expect(disclosure.points[0]).toContain('invoices/2026-09');
  });

  it('carries every fact the operator must decide on before anything is sent', () => {
    const copy = restoreDisclosure({ name: 'backup.tar.gz' }, 'invoices').points.join('\n');
    expect(copy).toContain('overwritten');
    expect(copy).toContain('directories merge');
    expect(copy).toContain('nothing else is deleted');
    expect(copy).toContain('one operation');
    expect(copy).toContain('never stored');
    expect(copy).toContain('partial');
    expect(copy).toContain('no transaction');
    expect(copy).toContain('entries already written stay');
    expect(copy).toContain('1 GiB compressed');
    expect(copy).toContain('4 GiB uncompressed');
    expect(copy).toContain('10,000 entries');
  });

  it('states the caps from the constants the client check uses', () => {
    expect(RESTORE_MAX_COMPRESSED_BYTES).toBe(1 << 30);
    expect(RESTORE_MAX_UNCOMPRESSED_BYTES).toBe(4 * (1 << 30));
    expect(RESTORE_MAX_ENTRIES).toBe(10_000);
  });
});

describe('restoreFailure', () => {
  it('names the 1 GiB compressed cap the API refused the body for', () => {
    const failure = restoreFailure(new ApiError(413, 'restore exceeds the 1 GiB compressed cap'));
    expect(failure.message).toBe('The archive exceeds the 1 GiB compressed cap');
    expect(failure.description).toContain('1 GiB');
    expect(failure.description).toContain('nothing was extracted');
    expect(failure.description).toContain('restore exceeds the 1 GiB compressed cap');
  });

  it('keeps the helper’s archive refusal and states that extraction is partial', () => {
    const refusal =
      'volume extract refused: relist-volhelper: extract "restored": unsafe archive entry: entry "../escape" has a ".." segment';
    const failure = restoreFailure(new ApiError(422, refusal));
    expect(failure.message).toBe('The Volume refused the archive');
    expect(failure.description).toContain('../escape');
    expect(failure.description).toContain('partial');
    expect(failure.description).toContain('entries already written stay');
  });

  it('names the space refusal and keeps the helper’s need-vs-available numbers', () => {
    const refusal =
      'volume extract refused: relist-volhelper: extract "restored" needs 2147483648 bytes but the Volume has only 1048576 free';
    const failure = restoreFailure(new ApiError(507, refusal));
    expect(failure.message).toBe('The Volume is out of space');
    expect(failure.description).toContain('needs 2147483648 bytes');
    expect(failure.description).toContain('1048576 free');
    expect(failure.description).toContain('no rollback');
    // The write family's promise — a refused write removes its partial file —
    // is not true of an extraction.
    expect(failure.description).not.toContain('removes its partial file');
  });

  it('explains the 409 missing source instead of leaving a bare conflict', () => {
    const failure = restoreFailure(
      new ApiError(409, 'volume demo_pgdata is missing: named volume "demo_pgdata" does not exist'),
    );
    expect(failure.message).toBe('This Volume’s source is missing');
    expect(failure.description).toContain('does not exist');
    expect(failure.description).toContain('mounting it is what would create it');
  });

  it('keeps the API’s 400 wording for a request the shape rules refused', () => {
    const failure = restoreFailure(new ApiError(400, 'path must be relative to the volume root'));
    expect(failure.message).toBe('The restore was refused');
    expect(failure.description).toBe('path must be relative to the volume root');
  });

  it('falls back to the API’s own message for an unmapped failure', () => {
    const failure = restoreFailure(new ApiError(502, 'volume extract failed: docker: exit status 1: permission denied'));
    expect(failure.message).toBe('Restore failed');
    expect(failure.description).toBe('volume extract failed: docker: exit status 1: permission denied');
  });

  it('says something for a rejection that is not an Error', () => {
    expect(restoreFailure('boom').description).toBe('boom');
  });
});

describe('newEntryNameError', () => {
  const entries = [
    entry({ name: 'notes.txt' }),
    entry({ name: 'incoming', kind: 'directory', mode: 0o040755 }),
  ];

  it('silently keeps Create disabled while the name is empty', () => {
    expect(newEntryNameError('directory', '', entries)).toBeUndefined();
    expect(newEntryNameError('file', '', entries)).toBeUndefined();
  });

  it('accepts any name that is one new entry', () => {
    expect(newEntryNameError('directory', 'uploads', entries)).toBeUndefined();
    expect(newEntryNameError('file', 'a b\nc.txt', entries)).toBeUndefined();
    expect(newEntryNameError('directory', 'x'.repeat(255), entries)).toBeUndefined();
  });

  it('refuses a name that could never be one entry, naming the kind', () => {
    expect(newEntryNameError('directory', '   ', entries)).toBe('Enter a folder name.');
    expect(newEntryNameError('file', '   ', entries)).toBe('Enter a file name.');
    expect(newEntryNameError('directory', 'a/b', entries)).toContain('“/”');
    expect(newEntryNameError('file', 'a/b', entries)).toContain('one file at a time');
    expect(newEntryNameError('file', '.', entries)).toBe('“.” is not a name.');
    expect(newEntryNameError('file', '..', entries)).toBe('“..” is not a name.');
    expect(newEntryNameError('directory', 'x'.repeat(256), entries)).toBe('Use 255 characters or fewer.');
  });

  it('refuses a name the listing already shows, file or directory', () => {
    expect(newEntryNameError('file', 'notes.txt', entries)).toBe('An entry named “notes.txt” already exists here.');
    expect(newEntryNameError('directory', 'incoming', entries)).toBe('An entry named “incoming” already exists here.');
  });
});

describe('newEntryFailure', () => {
  it('words an exists collision as the actionable refusal it is, per kind', () => {
    const api = 'directory already exists: relist-volhelper: mkdir "uploads": file exists';
    expect(newEntryFailure('directory', new ApiError(409, api))).toEqual({
      message: 'A folder with that name already exists',
      description: `${api} — names in a directory are unique, so the create was refused rather than replacing what was there.`,
    });
    expect(newEntryFailure('file', new ApiError(409, 'file already exists: create "notes.txt": file exists')).message).toBe(
      'A file with that name already exists',
    );
    expect(isExistsRefusal(api)).toBe(true);
    expect(isExistsRefusal('volume demo_pgdata is missing: no such volume')).toBe(false);
  });

  it('keeps the write family’s wording for the other statuses', () => {
    expect(newEntryFailure('directory', new ApiError(507, 'out of space')).message).toBe('The Volume is out of space');
    expect(newEntryFailure('file', new ApiError(409, 'volume demo_pgdata is missing: gone'))).toEqual({
      message: 'This Volume’s source is missing',
      description: 'volume demo_pgdata is missing: gone — mounting it is what would create it, so the panel refuses.',
    });
    expect(newEntryFailure('file', new ApiError(400, 'path must name a file or directory, not the volume root')).message).toBe(
      'The new file was refused',
    );
    expect(newEntryFailure('directory', new ApiError(502, 'volume mkdir failed: permission denied'))).toEqual({
      message: 'New folder failed',
      description: 'volume mkdir failed: permission denied',
    });
    expect(newEntryFailure('file', new Error('boom')).message).toBe('New file failed');
  });
});
