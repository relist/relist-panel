import { createContext, useContext, useEffect, useId, useMemo, useReducer, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { BOOT_CAP_MS, bootWaitingOn, CHECKING_SIGN_IN } from './boot-readiness';
import { BootSplash } from './BootSplash';

/** How long the splash takes to fade once the panel is ready. */
export const BOOT_FADE_MS = 240;

type Holds = {
  set: (id: string, waitingOn: string | null) => void;
  release: (id: string) => void;
};

const BootContext = createContext<Holds | undefined>(undefined);

/**
 * Holds the boot splash while `waitingOn` names what this screen is still
 * loading; `null` lets go. A no-op outside a `BootGate`, and after the panel
 * has shown once.
 */
export function useBootHold(waitingOn: string | null) {
  const holds = useContext(BootContext);
  const id = useId();
  useEffect(() => {
    holds?.set(id, waitingOn);
  }, [holds, id, waitingOn]);
  useEffect(() => () => holds?.release(id), [holds, id]);
}

type Phase = 'waiting' | 'leaving' | 'done';

/**
 * The first paint after sign-in: the app mounts underneath at once, so its own
 * loads start, while the splash covers it until the Migration lock has been
 * read and every `useBootHold` has let go (`boot-readiness.ts`). It then fades
 * and unmounts for the rest of this gate's life — later reloads belong to their
 * controls (GUIDELINES §8).
 *
 * Readiness is judged in this gate's effect, which React runs after its
 * children's: by then every hold of the commit has been registered, so a hand
 * over between two holds within one commit cannot open a gap.
 */
export function BootGate({ lockSettled, children }: { lockSettled: boolean; children: ReactNode }) {
  const registered = useRef(new Map<string, string | null>());
  const [version, changed] = useReducer((n: number) => n + 1, 0);
  const holds = useMemo<Holds>(() => ({
    set: (id, waitingOn) => {
      if (registered.current.has(id) && registered.current.get(id) === waitingOn) return;
      registered.current.set(id, waitingOn);
      changed();
    },
    release: (id) => {
      registered.current.delete(id);
      changed();
    },
  }), []);
  const [phase, setPhase] = useState<Phase>('waiting');
  const [label, setLabel] = useState(CHECKING_SIGN_IN);
  const [capped, setCapped] = useState(false);

  useEffect(() => {
    const timer = setTimeout(() => setCapped(true), BOOT_CAP_MS);
    return () => clearTimeout(timer);
  }, []);

  useEffect(() => {
    if (phase !== 'waiting') return;
    const waitingOn = bootWaitingOn({ lockSettled, holds: registered.current.values(), capped });
    if (waitingOn === null) {
      setPhase('leaving');
    } else {
      setLabel(waitingOn);
    }
  }, [capped, lockSettled, phase, version]);

  useEffect(() => {
    if (phase !== 'leaving') return;
    const timer = setTimeout(() => setPhase('done'), BOOT_FADE_MS);
    return () => clearTimeout(timer);
  }, [phase]);

  return (
    <BootContext.Provider value={phase === 'waiting' ? holds : undefined}>
      {/* Underneath the splash the app is unreachable by keyboard, too. */}
      <div inert={phase === 'waiting'}>{children}</div>
      {phase !== 'done' && <BootSplash fadeMs={BOOT_FADE_MS} label={label} leaving={phase === 'leaving'} />}
    </BootContext.Provider>
  );
}
