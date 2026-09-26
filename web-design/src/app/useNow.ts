import { createContext, useContext, useEffect, useState } from 'react';

/**
 * A ticking clock, for the idle countdown.
 *
 * Isolated in its own module so the countdown stays the only thing in the
 * redesign that re-renders on a timer: one shared ticker per screen, rather than
 * one interval per open Volume Handle.
 *
 * Ticket 10 adds the scoped half: inside a `SceneScopeProvider` the clock is a
 * frozen instant carried by context (the frame's `?clock=` equivalent), so
 * `useNow` returns it and never starts a ticker — a hundred frames must not be
 * a hundred intervals, and a frame's "now" must not drift from its store's
 * seeded timestamps. Standalone the context is absent and the wall clock ticks
 * exactly as before (a `?clock=` page is frozen anyway by `scene.ts`'s `Date`
 * patch, which this hook reads through `Date.now()` without knowing it).
 */

/** The frame's frozen instant in epoch milliseconds, or none outside a scope. */
export const SceneClockContext = createContext<number | undefined>(undefined);

export function useNow(intervalMs = 1_000): number {
  const frozen = useContext(SceneClockContext);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (frozen !== undefined) return undefined;
    const id = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(id);
  }, [frozen, intervalMs]);

  return frozen ?? now;
}
