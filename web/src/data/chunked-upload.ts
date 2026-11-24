/**
 * The client half of a chunked upload (.scratch/chunked-upload/spec.md,
 * ADR-0053): it slices a picked file into the server's chunk size, sends the
 * chunks in order from a known offset, retries a chunk that fails on the
 * network with backoff, waits out an offline browser, then asks the server to
 * write the file and polls until the write is done or has failed. Everything
 * it touches is injected, so it runs under test with no server and no clock.
 */
import type { VolumeUpload } from './types';

export type UploadTransport = {
  append(offset: number, chunk: Blob, signal: AbortSignal): Promise<{ offset: number; conflict: boolean }>;
  complete(): Promise<VolumeUpload | { state: 'consuming' }>;
  status(): Promise<VolumeUpload>;
};

export type UploadPhase = 'uploading' | 'offline' | 'writing';

/** The retries ran out: the upload stays on the server, resumable from `offset`. */
export class UploadPausedError extends Error {
  readonly offset: number;
  readonly cause: unknown;

  constructor(offset: number, cause: unknown) {
    super('Upload paused: the connection keeps failing');
    this.name = 'UploadPausedError';
    this.offset = offset;
    this.cause = cause;
  }
}

/** The server's chunk size (chunkupload.ChunkSize); a resume uses it without asking again. */
export const CHUNK_SIZE = 32 * 1024 * 1024;

/** Waits before each retry of a chunk; after the last, the upload pauses. */
export const RETRY_DELAYS_MS = [1000, 2000, 4000, 8000, 16000];
/** How often a write into the Volume is polled once every byte is sent. */
export const POLL_INTERVAL_MS = 2000;

const realSleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const realIsOnline = () => (typeof navigator === 'undefined' ? true : navigator.onLine);
const realWaitOnline = () =>
  new Promise<void>((resolve) => window.addEventListener('online', () => resolve(), { once: true }));

function abortError(): Error {
  return new DOMException('The upload was cancelled', 'AbortError');
}

/** Waits for promise, or rejects with an AbortError the moment signal aborts. */
function untilAborted<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) return Promise.reject(abortError());
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(abortError());
    signal.addEventListener('abort', onAbort, { once: true });
    promise.then(
      (value) => {
        signal.removeEventListener('abort', onAbort);
        resolve(value);
      },
      (error: unknown) => {
        signal.removeEventListener('abort', onAbort);
        reject(error);
      },
    );
  });
}

/**
 * Whether a failed request is worth sending again. The network failing, a
 * proxy or a restarting panel answering 5xx (Traefik's 502, Cloudflare's 52x),
 * and a 409 (another request holds the upload, or the server already moved
 * past this offset) all pass with time; any other answer from the API — 400,
 * 401, 404, 413 — is final.
 */
export function isRetryable(error: unknown): boolean {
  if (!(error instanceof Error) || error.name !== 'ApiError') return true;
  const status = (error as Error & { status?: number }).status ?? 0;
  return status >= 500 || status === 409;
}

export async function runChunkedUpload(opts: {
  file: Blob;
  chunkSize: number;
  offset: number;
  transport: UploadTransport;
  signal: AbortSignal;
  onProgress: (sent: number) => void;
  onPhase: (phase: UploadPhase) => void;
  sleep?: (ms: number) => Promise<void>;
  waitOnline?: () => Promise<void>;
  isOnline?: () => boolean;
}): Promise<VolumeUpload> {
  const { file, chunkSize, transport, signal, onProgress, onPhase } = opts;
  // Waiting — a backoff, an offline browser, a poll — ends the moment the
  // upload is cancelled, not when the wait would have.
  const baseSleep = opts.sleep ?? realSleep;
  const baseWaitOnline = opts.waitOnline ?? realWaitOnline;
  const sleep = (ms: number) => untilAborted(baseSleep(ms), signal);
  const waitOnline = () => untilAborted(baseWaitOnline(), signal);
  const isOnline = opts.isOnline ?? realIsOnline;

  let offset = opts.offset;
  onPhase('uploading');
  onProgress(offset);
  while (offset < file.size) {
    let attempt = 0;
    for (;;) {
      if (signal.aborted) throw abortError();
      if (!isOnline()) {
        onPhase('offline');
        await waitOnline();
        onPhase('uploading');
      }
      try {
        const answer = await transport.append(offset, file.slice(offset, offset + chunkSize), signal);
        offset = answer.offset;
        break;
      } catch (error) {
        if (signal.aborted) throw abortError();
        if (!isRetryable(error)) throw error;
        if (attempt >= RETRY_DELAYS_MS.length) throw new UploadPausedError(offset, error);
        await sleep(RETRY_DELAYS_MS[attempt]);
        attempt += 1;
      }
    }
    onProgress(offset);
  }

  // From here the bytes are all on the panel and the write is the server's:
  // the page shows it as writing, with nothing left to cancel.
  onPhase('writing');
  // complete, retried like a chunk; when its answer is lost, the status says
  // whether the write started, so it is never asked for twice.
  let completed: VolumeUpload | { state: 'consuming' } | undefined;
  for (let attempt = 0; completed === undefined; attempt += 1) {
    if (signal.aborted) throw abortError();
    try {
      completed = await transport.complete();
    } catch (error) {
      if (!isRetryable(error)) throw error;
      if (attempt >= RETRY_DELAYS_MS.length) throw new UploadPausedError(offset, error);
      await sleep(RETRY_DELAYS_MS[attempt]);
      const current = await transport.status().catch(() => undefined);
      if (current && current.state !== 'uploading') completed = current;
    }
  }
  if (completed.state !== 'consuming') return completed as VolumeUpload;

  // The write runs on the server whatever happens here: a failed check is
  // looked at again, and only a final answer (the upload is gone) stops it.
  for (;;) {
    await sleep(POLL_INTERVAL_MS);
    try {
      const current = await transport.status();
      if (current.state !== 'consuming') return current;
    } catch (error) {
      if (!isRetryable(error)) throw error;
    }
  }
}
