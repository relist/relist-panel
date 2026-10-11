import { useCallback, useEffect, useState } from 'react';

import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import { isActive } from '../data/deployments-view';
import type { ActiveDeployment } from '../data/deployments-view';
import type { Deployment } from '../data/types';

const POLL_MS = 1000;

/**
 * The Compose's newest Deployment, for the header button: read on load and
 * after `refresh()`, then re-read every second for as long as it is queued or
 * running. `running` names what holds the queue while this one waits.
 */
export function useLatestDeployment(path: ComposePath | undefined): {
  latest?: Deployment;
  refresh: () => void;
  running?: ActiveDeployment;
} {
  const [latest, setLatest] = useState<{ composeId: number; deployment?: Deployment }>();
  const [running, setRunning] = useState<ActiveDeployment>();
  const [round, setRound] = useState(0);
  const refresh = useCallback(() => setRound((n) => n + 1), []);

  const projectId = path?.projectId;
  const environmentId = path?.environmentId;
  const composeId = path?.composeId;
  useEffect(() => {
    if (projectId === undefined || environmentId === undefined || composeId === undefined) {
      return;
    }
    const location = { composeId, environmentId, projectId };
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      let again = false;
      try {
        const newest = (await api.listDeployments(location))[0];
        if (stopped) return;
        setLatest({ composeId, deployment: newest });
        again = newest !== undefined && isActive(newest);
        if (newest?.status === 'queued') {
          const queue = await api.deploymentQueue().catch(() => []);
          if (stopped) return;
          const head = queue.find((q) => q.status === 'running');
          setRunning(head ? { composeName: head.composeName, number: head.number, step: head.step } : undefined);
        }
      } catch {
        // Keep the last answer; nothing is active as far as we know.
      }
      if (!stopped && again) {
        timer = setTimeout(() => void load(), POLL_MS);
      }
    };
    void load();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [composeId, environmentId, projectId, round]);

  return { latest: latest?.composeId === composeId ? latest?.deployment : undefined, refresh, running };
}
