import { useEffect, useState } from 'react';

import { api } from './api';
import type { RemovalPlanEntry } from './types';

type PlanRead = { entries?: RemovalPlanEntry[]; error?: string; path: string };

/**
 * Reads the removal plan at `path` whenever it becomes defined, so a Delete
 * modal passes its URL only while open and sees a fresh plan each time. A
 * failed read is reported, never thrown: the typed confirmation stays usable
 * because the server computes its own plan when the delete runs.
 */
export function useRemovalPlan(path: string | undefined): { entries?: RemovalPlanEntry[]; error?: string; loading: boolean } {
  const [read, setRead] = useState<PlanRead>();

  useEffect(() => {
    if (path === undefined) {
      setRead(undefined); // a reopened modal must not flash the last plan
      return;
    }
    let stale = false;
    api
      .removalPlan(path)
      .then((plan) => {
        if (!stale) setRead({ entries: plan.composes, path });
      })
      .catch((reason: unknown) => {
        if (!stale) setRead({ error: reason instanceof Error ? reason.message : 'Could not load the removal plan', path });
      });
    return () => {
      stale = true;
    };
  }, [path]);

  if (path === undefined || read?.path !== path) {
    return { loading: path !== undefined };
  }
  return { entries: read.entries, error: read.error, loading: false };
}
