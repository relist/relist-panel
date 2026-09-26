import { useEffect, useState } from 'react';

/**
 * A ticking clock, for the idle countdown.
 *
 * Isolated in its own module so the countdown stays the only thing in the
 * redesign that re-renders on a timer: one shared ticker per screen, rather than
 * one interval per open Volume Handle.
 */
export function useNow(intervalMs = 1_000): number {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs]);

  return now;
}
