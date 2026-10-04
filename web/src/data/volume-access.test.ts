import { afterEach, describe, expect, it, vi } from 'vitest';
import { volumeAccessReady } from '../components/volumes-view';
import { editorReadCurrent, editorSessionCurrent, savedEditorState } from '../pages/volume-explorer-view';
import { api, volumeAccessQuery, volumeArchiveUrl, volumeContentUrl, volumeDirectoryUrl, volumeFileUrl, volumeReadUrl, volumeRestoreUrl } from './api';

describe('Volume operation identity', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('forwards the selected override and acknowledgements for list, read, upload, delete, create and restore', async () => {
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) => new Response('[]', { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    const location = { projectId: 1, environmentId: 2, composeId: 3 };
    const access = { user: '0:1000', rootConfirmed: true, bindConfirmed: true };
    await api.listVolumeEntries(location, 'data', 'config', access);
    await api.readVolumeFile(location, 'data', 'config', access);
    await api.writeVolumeFile(location, 'data', 'config', new Blob(['bytes']), access);
    await api.deleteVolumeEntry(location, 'data', 'config', access);
    await api.createVolumeDirectory(location, 'data', 'config', access);
    await api.createVolumeFile(location, 'data', 'config', access);
    await api.restoreVolume(location, 'data', 'config', new Blob(['archive']), access);
    expect(fetch).toHaveBeenCalledTimes(7);
    for (const call of fetch.mock.calls) {
      const query = new URL(String(call[0]), 'http://panel').searchParams;
      expect(query.get('user')).toBe('0:1000');
      expect(query.get('rootConfirmed')).toBe('1');
      expect(query.get('bindConfirmed')).toBe('1');
    }
  });

  it('requires a chosen numeric identity and separate root and bind acknowledgements', () => {
    expect(volumeAccessReady(undefined, false, {})).toBe(false);
    expect(volumeAccessReady('0:1000', false, {})).toBe(false);
    expect(volumeAccessReady('000:1000', true, { rootConfirmed: true })).toBe(false);
    expect(volumeAccessReady('0:1000', true, { rootConfirmed: true, bindConfirmed: true })).toBe(true);
    expect(volumeAccessReady('1000:1001', false, {})).toBe(true);
    expect(volumeAccessReady('1000:1001', true, {})).toBe(false);
    expect(volumeAccessReady('4294967296:1', false, {})).toBe(false);
  });

  it('carries identity and acknowledgements through every operation URL', () => {
    const location = { projectId: 1, environmentId: 2, composeId: 3 };
    const access = { service: 'worker', rootConfirmed: true, bindConfirmed: true };
    for (const builder of [volumeArchiveUrl, volumeContentUrl, volumeDirectoryUrl, volumeFileUrl, volumeReadUrl, volumeRestoreUrl]) {
      const query = new URL(builder(location, 'data', 'config', access), 'http://panel').searchParams;
      expect(query.get('service')).toBe('worker');
      expect(query.get('rootConfirmed')).toBe('1');
      expect(query.get('bindConfirmed')).toBe('1');
    }
    expect(volumeAccessQuery({ user: '1000:1001', service: 'ignored' })).toBe('&user=1000%3A1001');
  });

  it('rejects read completions after navigating or changing the selected identity or acknowledgements', () => {
    const opening = { request: 1, context: 'compose:1/source:data/user:1000:1000/root:0/bind:1' };
    expect(editorReadCurrent(opening, { ...opening })).toBe(true);
    for (const context of [
      'compose:2/source:data/user:1000:1000/root:0/bind:1',
      'compose:1/source:other-data/user:1000:1000/root:0/bind:1',
      'compose:1/source:data/user:2000:2000/root:0/bind:1',
      'compose:1/source:data/user:1000:1000/root:0/bind:0',
    ]) {
      expect(editorReadCurrent(opening, { request: 1, context })).toBe(false);
    }
    expect(editorReadCurrent(opening, { request: 2, context: opening.context })).toBe(false);
  });

  it('keeps edits typed while the saved snapshot was in flight dirty', () => {
    expect(savedEditorState({ session: 1, path: 'config', original: 'old', draft: 'newer' }, 1, 'sent')).toEqual({
      session: 1,
      path: 'config',
      original: 'sent',
      draft: 'newer',
    });
  });

  it('ignores a failed save after its editor is closed or replaced', () => {
    expect(editorSessionCurrent({ session: 1 }, 1)).toBe(true);
    expect(editorSessionCurrent(undefined, 1)).toBe(false);
    expect(editorSessionCurrent({ session: 2 }, 1)).toBe(false);
  });

  it('does not apply an old save to the same file reopened in a new editor session', () => {
    const reopened = { session: 2, path: 'config', original: 'reopened', draft: 'new edits' };
    expect(savedEditorState(reopened, 1, 'sent')).toBe(reopened);
  });

  it('does not apply an old save to the same relative path opened from another Volume', () => {
    const otherVolume = { session: 3, source: 'other-data', path: 'config', original: 'other bytes', draft: 'other edits' };
    expect(savedEditorState(otherVolume, 1, 'sent')).toBe(otherVolume);
  });
});
