/**
 * The declared `?ui=` keys — the single place they live.
 *
 * A scene forces state by name (`?ui=compose.logsOpen=1&ui=compose.tab=logs`),
 * and that name is the only thing tying the URL to a screen: a typo is a silent
 * no-op unless something knows which keys exist. This list is that something —
 * `scene.ts` warns in dev for a key that is not here, `scripts/scene.test.mjs`
 * covers the detection, and ticket 03 asserts that every key a scene declares
 * appears in this file.
 *
 * Two families, because the mock forces state in two ways:
 *
 *  - **screen keys** are read by `useUiState`, one per converted `useState`
 *    site. Ticket 02 fills `SCREEN_UI_KEYS` in as it does the swap.
 *  - **store keys** are read by `sceneOverrides()`, not by a screen: storage
 *    state (an open Volume Handle, the mock's latency, the activity log) is a
 *    prop on `PanelProvider` rather than a hook (spec: *Forcing state without a
 *    click*), so a scene names it here and the harness turns it into that prop.
 */

/** Screen keys: one per `useUiState` call site. Ticket 02 fills this in. */
export const SCREEN_UI_KEYS: readonly string[] = [];

/**
 * Store keys, as an object rather than a list so `sceneOverrides()` reads them
 * by name and the declaration below cannot drift from what it reads.
 */
export const STORE_UI_KEYS = {
  /** `store.activity=empty` — the activity feed, emptied. */
  activity: 'store.activity',
  /** `store.handles=<volumeId>[,<volumeId>]` or `none` — which Volume Handles are open. */
  handles: 'store.handles',
  /** `store.speed=instant|normal|slow` — the mock's latency. */
  speed: 'store.speed',
} as const;

export const STORE_UI_KEY_LIST: readonly string[] = Object.values(STORE_UI_KEYS);

export const DECLARED_UI_KEYS: readonly string[] = [...SCREEN_UI_KEYS, ...STORE_UI_KEY_LIST];

/** The keys in `keys` that no screen and no store override declares, in order, without repeats. */
export function undeclaredUiKeys(keys: Iterable<string>, declared: readonly string[] = DECLARED_UI_KEYS): string[] {
  const known = new Set(declared);
  return [...new Set(keys)].filter((key) => !known.has(key));
}

/**
 * A warner that names an undeclared key **once** per page: `?ui=` is read once,
 * so warning per render would say the same thing forty times.
 *
 * The `warn` sink is a parameter because this module is imported by the tests
 * under plain Node, where "dev mode" does not exist: `scene.ts` passes
 * `console.warn` when it is running under `vite dev`, and the tests pass an
 * array.
 */
export function createUndeclaredUiKeyWarner(
  warn: (message: string) => void,
  declared: readonly string[] = DECLARED_UI_KEYS,
): (keys: Iterable<string>) => void {
  const warned = new Set<string>();
  return (keys) => {
    for (const key of undeclaredUiKeys(keys, declared)) {
      if (warned.has(key)) continue;
      warned.add(key);
      warn(`?ui=${key} is not declared in app/uiKeys.ts — no screen reads it, so it forces nothing.`);
    }
  };
}
