import { useCallback, useEffect, useRef, useState } from 'react';

import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import { isActive } from '../data/deployments-view';
import type { ActiveDeployment } from '../data/deployments-view';
import type { Deployment } from '../data/types';

const POLL_MS = 1000;
const SLOW_POLL_MS = 5000;
const FAILURES_BEFORE_SLOW = 3;

/**
 * The Compose's Deployment history, read once per page: on load and after
 * `refresh()`, then every second for as long as any Deployment is queued or
 * running (and nothing otherwise). Failed reads retry only while something is
 * known to be active, backing off to 5 s after three in a row.
 *
 * `onFinished` fires when a Deployment seen active leaves queued/running, even
 * if the Deployments tab was never opened. `running` names what holds the
 * queue while a Deployment waits.
 */
export function useDeploymentHistory(
  path: ComposePath | undefined,
  onFinished: () => void,
): { history?: Deployment[]; latest?: Deployment; refresh: () => void; running?: ActiveDeployment } {
  const [state, setState] = useState<{ composeId: number; history: Deployment[] }>();
  const [running, setRunning] = useState<ActiveDeployment>();
  const [round, setRound] = useState(0);
  const refresh = useCallback(() => setRound((n) => n + 1), []);

  const onFinishedRef = useRef(onFinished);
  onFinishedRef.current = onFinished;
  // Numbers last seen active, per Compose; they survive a `refresh()`.
  const activeSeen = useRef<{ composeId?: number; numbers: Set<number> }>({ numbers: new Set() });

  const projectId = path?.projectId;
  const environmentId = path?.environmentId;
  const composeId = path?.composeId;
  useEffect(() => {
    if (projectId === undefined || environmentId === undefined || composeId === undefined) {
      return;
    }
    const location = { composeId, environmentId, projectId };
    if (activeSeen.current.composeId !== composeId) {
      activeSeen.current = { composeId, numbers: new Set() };
    }
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    const load = async () => {
      let again = false;
      try {
        const list = await api.listDeployments(location);
        if (stopped) return;
        failures = 0;
        setState({ composeId, history: list });
        if (list.some((d) => d.status === 'queued')) {
          const queue = await api.deploymentQueue().catch(() => []);
          if (stopped) return;
          const head = queue.find((q) => q.status === 'running');
          setRunning(head ? { composeName: head.composeName, number: head.number, step: head.step } : undefined);
        }
        const before = activeSeen.current.numbers;
        const now = new Set(list.filter(isActive).map((d) => d.number));
        activeSeen.current.numbers = now;
        again = now.size > 0;
        if (list.some((d) => before.has(d.number) && !now.has(d.number))) {
          onFinishedRef.current();
        }
      } catch {
        failures += 1;
        again = activeSeen.current.numbers.size > 0;
      }
      if (!stopped && again) {
        timer = setTimeout(() => void load(), failures >= FAILURES_BEFORE_SLOW ? SLOW_POLL_MS : POLL_MS);
      }
    };
    void load();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [composeId, environmentId, projectId, round]);

  const history = state?.composeId === composeId ? state?.history : undefined;
  return { history, latest: history?.[0], refresh, running };
}
