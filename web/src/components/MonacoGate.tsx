import { Alert, Button } from 'antd';
import { useEffect, useSyncExternalStore } from 'react';

/**
 * Monaco, its YAML worker and the Compose schema are most of the bundle, so
 * they load on demand: the first editor that mounts imports `../monaco` (which
 * configures `@monaco-editor/react`'s loader) and every editor waits for it.
 * `@monaco-editor/react` must not mount before that, or its loader would fetch
 * Monaco from a CDN instead of using the bundled copy.
 */

export type MonacoStatus = 'loading' | 'ready' | 'failed';

let status: MonacoStatus = 'loading';
let started = false;
const listeners = new Set<() => void>();

function set(next: MonacoStatus) {
  status = next;
  listeners.forEach((listener) => listener());
}

/** Starts loading Monaco once; later calls do nothing. */
export function loadMonaco() {
  if (started) return;
  started = true;
  import('../monaco').then(
    () => set('ready'),
    (error: unknown) => {
      console.error('load the editor', error);
      set('failed');
    },
  );
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Starts loading Monaco on mount and reports how far it got. */
export function useMonaco(): MonacoStatus {
  useEffect(loadMonaco, []);
  return useSyncExternalStore(subscribe, () => status);
}

/**
 * What an editor shows until Monaco is ready: the same "Loading editor…" the
 * editor itself shows while it starts, at the editor's height. A failed import
 * almost always means a Panel update replaced the bundle after this page was
 * opened, and the browser will not retry a failed module, so the way out is a
 * reload.
 */
export function EditorPending({ height, status }: { height: number | string; status: MonacoStatus }) {
  if (status === 'failed') {
    return (
      <Alert
        action={
          <Button onClick={() => window.location.reload()} size="small">
            Reload
          </Button>
        }
        description="The panel may have been updated since this page was opened. Reload to get the current version."
        showIcon
        title="The editor could not load"
        type="error"
      />
    );
  }
  return (
    <div style={{ alignItems: 'center', display: 'flex', height, justifyContent: 'center', width: '100%' }}>
      <span>Loading editor…</span>
    </div>
  );
}
