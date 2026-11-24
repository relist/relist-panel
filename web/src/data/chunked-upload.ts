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

/** A failure the API answered (an ApiError) is final; anything else is the network. */
function isApiRefusal(error: unknown): boolean {
  return error instanceof Error && error.name === 'ApiError';
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
  const sleep = opts.sleep ?? realSleep;
  const waitOnline = opts.waitOnline ?? realWaitOnline;
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
        if (isApiRefusal(error)) throw error;
        if (attempt >= RETRY_DELAYS_MS.length) throw new UploadPausedError(offset, error);
        await sleep(RETRY_DELAYS_MS[attempt]);
        attempt += 1;
      }
    }
    onProgress(offset);
  }

  if (signal.aborted) throw abortError();
  const completed = await transport.complete();
  if (completed.state !== 'consuming') return completed;
  onPhase('writing');
  for (;;) {
    await sleep(POLL_INTERVAL_MS);
    const current = await transport.status();
    if (current.state !== 'consuming') return current;
  }
}
