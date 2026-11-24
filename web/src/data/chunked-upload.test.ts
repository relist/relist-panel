import { describe, expect, it, vi } from 'vitest';

import { UploadPausedError, runChunkedUpload, type UploadTransport } from './chunked-upload';
import type { VolumeUpload } from './types';

const MiB = 1 << 20;
const chunkSize = 32 * MiB;

function apiError(status: number): Error {
  return Object.assign(new Error(`HTTP ${status}`), { name: 'ApiError', status });
}

function upload(state: VolumeUpload['state'], size: number): VolumeUpload {
  return {
    id: 'u1',
    source: 'data',
    path: 'big.bin',
    name: 'big.bin',
    size,
    lastModified: 1,
    offset: size,
    state,
    errorKind: '',
    error: '',
    updatedAt: '2026-11-24T00:00:00Z',
  };
}

/** A transport that stores every append and answers like a healthy server. */
function fakeTransport(size: number, overrides: Partial<UploadTransport> = {}) {
  const offsets: number[] = [];
  const lengths: number[] = [];
  const transport: UploadTransport = {
    append: vi.fn(async (offset: number, chunk: Blob) => {
      offsets.push(offset);
      lengths.push(chunk.size);
      return { offset: offset + chunk.size, conflict: false };
    }),
    complete: vi.fn(async () => upload('done', size)),
    status: vi.fn(async () => upload('done', size)),
    ...overrides,
  };
  return { transport, offsets, lengths };
}

function run(file: Blob, transport: UploadTransport, extra: Partial<Parameters<typeof runChunkedUpload>[0]> = {}) {
  const sleeps: number[] = [];
  const progress: number[] = [];
  const phases: string[] = [];
  const promise = runChunkedUpload({
    file,
    chunkSize,
    offset: 0,
    transport,
    signal: new AbortController().signal,
    onProgress: (sent) => progress.push(sent),
    onPhase: (phase) => phases.push(phase),
    sleep: async (ms) => {
      sleeps.push(ms);
    },
    isOnline: () => true,
    waitOnline: async () => {},
    ...extra,
  });
  return { promise, sleeps, progress, phases };
}

describe('runChunkedUpload', () => {
  it('sends 80 MiB as 32, 32, 16', async () => {
    const file = new Blob([new Uint8Array(80 * MiB)]);
    const { transport, offsets, lengths } = fakeTransport(file.size);
    const { promise, progress } = run(file, transport);
    await expect(promise).resolves.toMatchObject({ state: 'done' });
    expect(offsets).toEqual([0, 32 * MiB, 64 * MiB]);
    expect(lengths).toEqual([32 * MiB, 32 * MiB, 16 * MiB]);
    expect(progress.at(-1)).toBe(80 * MiB);
  });

  it('resumes from a non-zero offset', async () => {
    const file = new Blob([new Uint8Array(40 * MiB)]);
    const { transport, offsets } = fakeTransport(file.size);
    const { promise } = run(file, transport, { offset: 32 * MiB });
    await promise;
    expect(offsets).toEqual([32 * MiB]);
  });

  it('follows a 409 offset forward', async () => {
    const file = new Blob([new Uint8Array(64 * MiB)]);
    const offsets: number[] = [];
    let first = true;
    const { transport } = fakeTransport(file.size, {
      append: vi.fn(async (offset: number, chunk: Blob) => {
        offsets.push(offset);
        if (first) {
          first = false;
          return { offset: 32 * MiB, conflict: true };
        }
        return { offset: offset + chunk.size, conflict: false };
      }),
    });
    await run(file, transport).promise;
    expect(offsets).toEqual([0, 32 * MiB]);
  });

  it('retries with backoff then pauses', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const { transport } = fakeTransport(file.size, {
      append: vi.fn(async () => {
        throw new TypeError('network');
      }),
    });
    const { promise, sleeps } = run(file, transport);
    const error = await promise.catch((e: unknown) => e);
    expect(error).toBeInstanceOf(UploadPausedError);
    expect((error as UploadPausedError).offset).toBe(0);
    expect(sleeps).toEqual([1000, 2000, 4000, 8000, 16000]);
    expect(transport.append).toHaveBeenCalledTimes(6);
  });

  it('does not retry an API refusal', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const refusal = Object.assign(new Error('upload not found'), { name: 'ApiError', status: 404 });
    const { transport } = fakeTransport(file.size, {
      append: vi.fn(async () => {
        throw refusal;
      }),
    });
    const { promise, sleeps } = run(file, transport);
    await expect(promise).rejects.toBe(refusal);
    expect(sleeps).toEqual([]);
  });

  it('waits while offline', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const { transport } = fakeTransport(file.size);
    let online = false;
    const waitOnline = vi.fn(async () => {
      online = true;
    });
    const { promise, phases } = run(file, transport, { isOnline: () => online, waitOnline });
    await promise;
    expect(waitOnline).toHaveBeenCalledTimes(1);
    expect(phases).toContain('offline');
  });

  it('small file completes without polling', async () => {
    const file = new Blob([new Uint8Array(1)]);
    const { transport } = fakeTransport(file.size);
    const { promise, sleeps } = run(file, transport);
    await expect(promise).resolves.toMatchObject({ state: 'done' });
    expect(transport.status).not.toHaveBeenCalled();
    expect(sleeps).toEqual([]);
  });

  it('polls every 2 s while writing', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const statuses = [upload('consuming', 10), upload('done', 10)];
    const { transport } = fakeTransport(file.size, {
      complete: vi.fn(async () => ({ state: 'consuming' as const })),
      status: vi.fn(async () => statuses.shift()!),
    });
    const { promise, sleeps, phases } = run(file, transport);
    await expect(promise).resolves.toMatchObject({ state: 'done' });
    expect(sleeps).toEqual([2000, 2000]);
    expect(phases).toContain('writing');
  });

  it('resolves a failed write instead of throwing', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const failed = { ...upload('failed', 10), errorKind: 'refused' as const, error: 'need 20' };
    const { transport } = fakeTransport(file.size, { complete: vi.fn(async () => failed) });
    await expect(run(file, transport).promise).resolves.toEqual(failed);
  });

  it('abort stops sending', async () => {
    const file = new Blob([new Uint8Array(64 * MiB)]);
    const controller = new AbortController();
    const { transport } = fakeTransport(file.size, {
      append: vi.fn(async (offset: number, chunk: Blob) => {
        controller.abort();
        return { offset: offset + chunk.size, conflict: false };
      }),
    });
    const error = await run(file, transport, { signal: controller.signal }).promise.catch((e: unknown) => e);
    expect((error as Error).name).toBe('AbortError');
    expect(transport.append).toHaveBeenCalledTimes(1);
    expect(transport.complete).not.toHaveBeenCalled();
  });

  it('retries a proxy 5xx and a busy 409 like a network error', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const answers = [apiError(502), apiError(409)];
    const { transport } = fakeTransport(file.size, {
      append: vi.fn(async (offset: number, chunk: Blob) => {
        const next = answers.shift();
        if (next) throw next;
        return { offset: offset + chunk.size, conflict: false };
      }),
    });
    const { promise, sleeps } = run(file, transport);
    await expect(promise).resolves.toMatchObject({ state: 'done' });
    expect(sleeps).toEqual([1000, 2000]);
  });

  it('keeps polling through a failed status check', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const statuses: Array<VolumeUpload | Error> = [new TypeError('network'), apiError(524), upload('done', 10)];
    const { transport } = fakeTransport(file.size, {
      complete: vi.fn(async () => ({ state: 'consuming' as const })),
      status: vi.fn(async () => {
        const next = statuses.shift()!;
        if (next instanceof Error) throw next;
        return next;
      }),
    });
    await expect(run(file, transport).promise).resolves.toMatchObject({ state: 'done' });
  });

  it('finds a write whose complete answer was lost', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const statuses = [upload('consuming', 10), upload('done', 10)];
    const { transport } = fakeTransport(file.size, {
      complete: vi.fn(async () => {
        throw apiError(524);
      }),
      status: vi.fn(async () => statuses.shift()!),
    });
    await expect(run(file, transport).promise).resolves.toMatchObject({ state: 'done' });
    expect(transport.complete).toHaveBeenCalledTimes(1);
  });

  it('cancel during a backoff stops at once', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const controller = new AbortController();
    const { transport } = fakeTransport(file.size, {
      append: vi.fn(async () => {
        throw new TypeError('network');
      }),
    });
    const promise = runChunkedUpload({
      file,
      chunkSize,
      offset: 0,
      transport,
      signal: controller.signal,
      onProgress: () => {},
      onPhase: () => {},
      sleep: () => new Promise(() => {}), // a backoff that would never end
      isOnline: () => true,
      waitOnline: async () => {},
    });
    await Promise.resolve();
    await Promise.resolve();
    controller.abort();
    await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('cancel while offline stops at once', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const controller = new AbortController();
    const { transport } = fakeTransport(file.size);
    const promise = runChunkedUpload({
      file,
      chunkSize,
      offset: 0,
      transport,
      signal: controller.signal,
      onProgress: () => {},
      onPhase: () => {},
      isOnline: () => false,
      waitOnline: () => new Promise(() => {}), // never back online
    });
    controller.abort();
    await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
    expect(transport.append).not.toHaveBeenCalled();
  });

  it('is writing, with no cancel, from the moment it asks to complete', async () => {
    const file = new Blob([new Uint8Array(10)]);
    const phases: string[] = [];
    let phaseAtComplete: string | undefined;
    const { transport } = fakeTransport(file.size, {
      complete: vi.fn(async () => {
        phaseAtComplete = phases.at(-1);
        return upload('done', 10);
      }),
    });
    await run(file, transport, { onPhase: (phase) => phases.push(phase) }).promise;
    expect(phaseAtComplete).toBe('writing');
  });
});
