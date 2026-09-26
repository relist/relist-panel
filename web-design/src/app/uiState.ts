import { useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';

/**
 * Forced state, read once from the URL: `?ui=compose.logsOpen=1&ui=compose.tab=logs`.
 *
 * This module is the *pure* half of the scene harness — the parse, the coercion
 * and the hook — and it is deliberately import-light (React and nothing else of
 * this app), because it is the half the unit tests import: `node --test` resolves
 * a TypeScript file only where every specifier is bare, so `scripts/scene.test.mjs`
 * can import this module but not `scene.ts`. `scene.ts` is the browser bootstrap:
 * it calls `parseSceneUrl` on `window.location.search` once, installs the result
 * here, freezes the clock, and re-exports what the app needs.
 *
 * **Read once is deliberate** (spec: *Forcing state without a click*). The map is
 * installed at module load, so editing `?ui=` in a live tab does nothing until the
 * next load — which is exactly what a capture, an *Open live* click and a pasted
 * URL are. Nothing listens for `popstate` or races the hash router, and the cost
 * is stated rather than hidden.
 */

/** What `?ui=` can force: a flag, or the raw string it carried. */
export type UiValue = boolean | string;

export type SceneUrl = {
  /** `?after=` — a named transition adapter. Parsed here, inert until ticket 02. */
  after: string | null;
  /** `?clock=` — the raw instant. `scene.ts` validates it before installing anything. */
  clock: string | null;
  /** `?scene=` — the id readiness echoes back on `<html>`. */
  scene: string | null;
  /** Every `?ui=` entry, coerced, in the order the URL carried it. */
  ui: ReadonlyMap<string, UiValue>;
};

/**
 * `1`/`true` and `0`/`false` are flags, because a forced `useState` boolean is
 * what a scene key usually is; anything else stays the string the URL carried.
 *
 * Numbers are deliberately *not* inferred: a scene key that wants `2` gets the
 * string `'2'`, and a screen whose state is a number needs a coercion rule of its
 * own — guessing here would turn `?ui=…=1` into a number for the boolean sites
 * that are the common case.
 */
export function coerceUiValue(raw: string): UiValue {
  if (raw === '1' || raw === 'true') return true;
  if (raw === '0' || raw === 'false') return false;
  return raw;
}

/**
 * `{ clock, scene, ui, after }` from a query string — no DOM, so the tests drive
 * it directly.
 *
 * A `ui` entry splits on its **first** `=` only, so a forced value may contain
 * one (a path, a filter); `?ui=key` with no `=` at all is a flag, because the URL
 * has no other way to spell "on" and `?ui=key=` already means the empty string.
 */
export function parseSceneUrl(search: string): SceneUrl {
  const params = new URLSearchParams(search);
  const ui = new Map<string, UiValue>();

  for (const entry of params.getAll('ui')) {
    const separator = entry.indexOf('=');
    const key = separator === -1 ? entry : entry.slice(0, separator);
    const raw = separator === -1 ? '1' : entry.slice(separator + 1);
    if (key !== '') ui.set(key, coerceUiValue(raw));
  }

  return { after: params.get('after'), clock: params.get('clock'), scene: params.get('scene'), ui };
}

let forced: ReadonlyMap<string, UiValue> = new Map();

/** Install the map `scene.ts` parsed at load. Called once; later calls are not a feature. */
export function installUiOverrides(ui: ReadonlyMap<string, UiValue>): void {
  forced = ui;
}

/** `useState`, plus one rule: a value the URL forced wins over `initial`. */
export function useUiState<T>(key: string, initial: T): [T, Dispatch<SetStateAction<T>>] {
  // The override is read when the state is created — once — so a later render
  // cannot flip a forced value back to the seed.
  return useState<T>(() => {
    const value = forced.get(key);
    return value === undefined ? initial : (value as unknown as T);
  });
}
