// @vitest-environment jsdom
import '../test/dom-setup';

import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import type { ComposeSource } from '../data/types';
import { useComposeSource } from './useComposeSource';

const path = { composeId: 7, environmentId: 2, projectId: 1 };
const otherPath = { composeId: 8, environmentId: 2, projectId: 1 };
const source: ComposeSource = {
  branch: 'main', headSha: '', kind: 'git', lastTest: null,
  sshKey: { id: 1, name: 'ci', publicKey: 'ssh-ed25519 AAAA relist-ci' }, repoUrl: 'git@github.com:acme/shop.git',
  submodules: false, sparsePaths: [], zip: null,
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => { resolve = onResolve; reject = onReject; });
  return { promise, reject, resolve };
}

function renderSource(initialPath: ComposePath | undefined = path) {
  const onError = vi.fn();
  const initialProps: { location: ComposePath | undefined } = { location: initialPath };
  return {
    onError,
    ...renderHook(({ location }: { location: ComposePath | undefined }) => useComposeSource(location, onError), {
      initialProps,
    }),
  };
}

afterEach(() => { vi.restoreAllMocks(); });

describe('useComposeSource', () => {
  it('keeps a current failure when an old Compose succeeds', async () => {
    const old = deferred<ComposeSource | null>();
    const current = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const hook = renderSource();
    hook.rerender({ location: otherPath });
    await act(async () => { current.reject(new Error('Current denied')); });
    await act(async () => { old.resolve(source); });
    expect(hook.result.current.state).toBe('failed');
    expect(hook.result.current.error).toBe('Current denied');
    expect(hook.result.current.source).toBeNull();
  });

  it('ignores an old Compose failure after the current Source loads', async () => {
    const old = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource').mockReturnValueOnce(old.promise).mockResolvedValueOnce(source);
    const hook = renderSource();
    await act(async () => { hook.rerender({ location: otherPath }); });
    await act(async () => { old.reject(new Error('Old denied')); });
    expect(hook.result.current.state).toBe('present');
    expect(hook.result.current.source).toEqual(source);
    expect(hook.result.current.error).toBeUndefined();
    expect(hook.onError).not.toHaveBeenCalled();
  });

  it.each(['success', 'failure'] as const)('ignores an older same-Compose refresh %s', async (outcome) => {
    const old = deferred<ComposeSource | null>();
    const current = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const hook = renderSource();
    act(() => { hook.result.current.refresh(); });
    await act(async () => { current.resolve(null); });
    await act(async () => {
      if (outcome === 'success') old.resolve(source);
      else old.reject(new Error('Old denied'));
    });
    expect(hook.result.current.state).toBe('none');
    expect(hook.result.current.source).toBeNull();
    expect(hook.result.current.error).toBeUndefined();
    expect(hook.onError).not.toHaveBeenCalled();
  });

  it('blocks deployment during a refresh and retains the known Source for its card and modal', async () => {
    const retry = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource').mockResolvedValueOnce(source).mockReturnValueOnce(retry.promise);
    const hook = renderSource();
    await act(async () => {});
    expect(hook.result.current.state).toBe('present');
    act(() => { hook.result.current.refresh(); });
    expect(hook.result.current.state).toBe('loading');
    expect(hook.result.current.source).toEqual(source);
    await act(async () => { retry.reject(new Error('Retry denied')); });
    expect(hook.result.current.state).toBe('failed');
    expect(hook.result.current.error).toBe('Retry denied');
    expect(hook.result.current.source).toEqual(source);
  });

  it.each(['projectId', 'environmentId'] as const)('hides the Source when %s changes without changing Compose ID', async (field) => {
    const current = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource').mockResolvedValueOnce(source).mockReturnValueOnce(current.promise);
    const hook = renderSource();
    await act(async () => {});
    hook.rerender({ location: { ...path, [field]: path[field] + 10 } });
    expect(hook.result.current.state).toBe('loading');
    expect(hook.result.current.source).toBeNull();
    await act(async () => { current.resolve(null); });
    expect(hook.result.current.state).toBe('none');
  });

  it('invalidates pending reads when Project or Environment context disappears', async () => {
    const old = deferred<ComposeSource | null>();
    const current = deferred<ComposeSource | null>();
    const getSource = vi.spyOn(api, 'getSource').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const hook = renderSource();
    hook.rerender({ location: undefined });
    act(() => { hook.result.current.refresh(); });
    await act(async () => { old.reject(new Error('Old denied')); });
    expect(hook.result.current.state).toBe('loading');
    expect(hook.result.current.source).toBeNull();
    expect(hook.onError).not.toHaveBeenCalled();
    expect(getSource).toHaveBeenCalledTimes(1);
    hook.rerender({ location: path });
    await act(async () => { current.resolve(null); });
    expect(hook.result.current.state).toBe('none');
    expect(hook.result.current.error).toBeUndefined();
  });

  it('does not reuse a loaded Source after its context disappears and returns', async () => {
    const current = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource').mockResolvedValueOnce(source).mockReturnValueOnce(current.promise);
    const hook = renderSource();
    await act(async () => {});
    hook.rerender({ location: undefined });
    hook.rerender({ location: path });
    expect(hook.result.current.state).toBe('loading');
    expect(hook.result.current.source).toBeNull();
    await act(async () => { current.resolve(null); });
  });

  it('drops a failed read after unmount without reporting it on the next page', async () => {
    const old = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource').mockReturnValue(old.promise);
    const hook = renderSource();
    hook.unmount();
    await act(async () => { old.reject(new Error('Unmounted denied')); });
    expect(hook.onError).not.toHaveBeenCalled();
  });

  it('ignores a modal refresh callback that completes after the page unmounts', async () => {
    const getSource = vi.spyOn(api, 'getSource')
      .mockResolvedValueOnce(source)
      .mockRejectedValue(new Error('Unmounted denied'));
    const hook = renderSource();
    await act(async () => {});
    const refresh = hook.result.current.refresh;
    hook.unmount();
    await act(async () => { refresh(); });
    expect(hook.onError).not.toHaveBeenCalled();
    expect(getSource).toHaveBeenCalledTimes(1);
  });
});
