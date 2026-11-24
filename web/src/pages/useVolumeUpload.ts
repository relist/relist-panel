import { useCallback, useEffect, useRef, useState } from 'react';

import { api, type ComposePath } from '../data/api';
import { CHUNK_SIZE, UploadPausedError, runChunkedUpload, type UploadPhase } from '../data/chunked-upload';
import type { VolumeAccess, VolumeUpload } from '../data/types';
import { matchOpenUpload, uploadFailure, type VolumeFailure } from './volume-explorer-view';

/** The upload being sent or written right now. */
export type ActiveUpload = {
  /** The upload's id, so the unfinished list leaves it out while it is sent. */
  id: string;
  name: string;
  total: number;
  sent: number;
  phase: UploadPhase;
  /** When this run started, and how much was already stored then: speed and time left count from here. */
  startedAt: number;
  startSent: number;
};

/** One picked file and where it goes. */
type Picked = { file: File; target: string };

/**
 * The Volume Explorer's chunked upload (ADR-0053): it starts or resumes an
 * upload for a picked file, drives the chunks, and keeps the states the page
 * shows — the resume question, the running upload, a pause after the retries
 * ran out, and a write that failed after every byte arrived. A refusal the API
 * answers goes to onError in the page's write-failure copy.
 */
export function useVolumeUpload(opts: {
  location: ComposePath;
  source: string;
  access?: VolumeAccess;
  onDone: (target: string) => void;
  onError: (failure: VolumeFailure) => void;
}) {
  const { location, source, access, onDone, onError } = opts;
  const { projectId, environmentId, composeId } = location;
  const [openUploads, setOpenUploads] = useState<VolumeUpload[]>([]);
  const [active, setActive] = useState<ActiveUpload>();
  const [resumePrompt, setResumePrompt] = useState<Picked & { match: VolumeUpload }>();
  const [paused, setPaused] = useState<Picked & { id: string; offset: number }>();
  const [failed, setFailed] = useState<Picked & { upload: VolumeUpload }>();
  const controller = useRef<AbortController>(undefined);
  const busy = active !== undefined;

  const refreshOpen = useCallback(async () => {
    try {
      setOpenUploads(await api.listVolumeUploads({ projectId, environmentId, composeId }));
    } catch {
      // The list is a convenience; a failure leaves it as it was.
    }
  }, [projectId, environmentId, composeId]);

  useEffect(() => {
    void refreshOpen();
  }, [refreshOpen]);

  // Leaving the Explorer inside the app stops sending, but keeps the upload on
  // the panel: coming back and picking the file again resumes it.
  useEffect(() => () => controller.current?.abort('leave'), []);

  // Leaving the page while bytes are moving would strand the upload; ask first.
  useEffect(() => {
    if (!busy) return undefined;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [busy]);

  const drive = useCallback(
    async ({ file, target }: Picked, id: string, offset: number, chunkSize: number) => {
      const where = { projectId, environmentId, composeId };
      const abort = new AbortController();
      controller.current = abort;
      setPaused(undefined);
      setFailed(undefined);
      setActive({ id, name: file.name, total: file.size, sent: offset, phase: 'uploading', startedAt: Date.now(), startSent: offset });
      try {
        const result = await runChunkedUpload({
          file,
          chunkSize,
          offset,
          signal: abort.signal,
          transport: {
            append: (at, chunk, signal) => api.appendVolumeUploadChunk(where, id, at, chunk, signal),
            complete: () => api.completeVolumeUpload(where, id),
            status: () => api.volumeUploadStatus(where, id),
          },
          onProgress: (sent) => setActive((current) => current && { ...current, sent }),
          onPhase: (phase) => setActive((current) => current && { ...current, phase }),
        });
        if (result.state === 'done') {
          onDone(target);
        } else {
          setFailed({ file, target, upload: result });
        }
      } catch (error) {
        if (error instanceof UploadPausedError) {
          setPaused({ file, target, id, offset: error.offset });
        } else if (error instanceof Error && error.name === 'AbortError') {
          // Only the Cancel button discards; leaving the page keeps it.
          if (abort.signal.reason === 'cancel') {
            await api.cancelVolumeUpload(where, id).catch(() => undefined);
          }
        } else {
          onError(uploadFailure(error));
        }
      } finally {
        controller.current = undefined;
        setActive(undefined);
        void refreshOpen();
      }
    },
    [projectId, environmentId, composeId, onDone, onError, refreshOpen],
  );

  const startFresh = useCallback(
    async (picked: Picked) => {
      try {
        const started = await api.startVolumeUpload(
          { projectId, environmentId, composeId },
          source,
          picked.target,
          picked.file,
          access,
        );
        await drive(picked, started.id, started.offset, started.chunkSize);
      } catch (error) {
        onError(uploadFailure(error));
      }
    },
    [projectId, environmentId, composeId, source, access, drive, onError],
  );

  /**
   * A picked file: continue the matching unfinished upload, or start one. An
   * empty file has no chunk to send, so it is one direct write.
   */
  const pick = useCallback(
    (file: File, target: string) => {
      if (file.size === 0) {
        api
          .writeVolumeFile({ projectId, environmentId, composeId }, source, target, file, access)
          .then(() => onDone(target))
          .catch((error: unknown) => onError(uploadFailure(error)));
        return;
      }
      const match = matchOpenUpload(openUploads, file, target);
      if (match) {
        setResumePrompt({ file, target, match });
        return;
      }
      void startFresh({ file, target });
    },
    [openUploads, startFresh, projectId, environmentId, composeId, source, access, onDone, onError],
  );

  const resume = useCallback(async () => {
    if (!resumePrompt) return;
    const { file, target, match } = resumePrompt;
    setResumePrompt(undefined);
    await drive({ file, target }, match.id, match.offset, CHUNK_SIZE);
  }, [resumePrompt, drive]);

  const startOver = useCallback(async () => {
    if (!resumePrompt) return;
    const { file, target, match } = resumePrompt;
    setResumePrompt(undefined);
    try {
      await api.cancelVolumeUpload({ projectId, environmentId, composeId }, match.id);
    } catch (error) {
      onError(uploadFailure(error));
      return;
    }
    await startFresh({ file, target });
  }, [resumePrompt, projectId, environmentId, composeId, startFresh, onError]);

  const resumePaused = useCallback(async () => {
    if (!paused) return;
    const { file, target, id, offset } = paused;
    await drive({ file, target }, id, offset, CHUNK_SIZE);
  }, [paused, drive]);

  /** Writes the stored file again: every byte is already on the panel. */
  const retry = useCallback(async () => {
    if (!failed) return;
    const { file, target, upload } = failed;
    await drive({ file, target }, upload.id, upload.size, CHUNK_SIZE);
  }, [failed, drive]);

  const discard = useCallback(
    async (id: string) => {
      try {
        await api.cancelVolumeUpload({ projectId, environmentId, composeId }, id);
      } catch (error) {
        onError(uploadFailure(error));
      }
      setPaused((current) => (current?.id === id ? undefined : current));
      setFailed((current) => (current?.upload.id === id ? undefined : current));
      await refreshOpen();
    },
    [projectId, environmentId, composeId, onError, refreshOpen],
  );

  return {
    openUploads,
    active,
    resumePrompt,
    paused,
    failed,
    pick,
    resume,
    startOver,
    dismissPrompt: () => setResumePrompt(undefined),
    cancel: () => controller.current?.abort('cancel'),
    resumePaused,
    retry,
    discard,
  };
}
