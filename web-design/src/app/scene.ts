/**
 * The scene harness — `?ui=`, `?clock=`, `?scene=`, `?after=`.
 *
 * `main.tsx`'s **first import**, and the order is load-bearing rather than tidy:
 * `data/volumeMocks.ts` calls `mtime()` at module scope through
 * `file()`/`dir()`/`symlink()`, so every fixture timestamp in the Volume Explorer
 * is computed while the module graph is still evaluating — before any statement of
 * `main.tsx` runs. Imported first, this module's body has already frozen the clock
 * by the time the fixtures read it. A clock installed in a provider, a hook or an
 * effect is too late for that one call site (and only for that one, which is what
 * makes it easy to miss).
 *
 * Everything else the app needs from the URL comes from here too — the hook, the
 * store overrides, the scene id — so there is one parse and one place where load
 * order matters.
 */
import type { SceneOverrides } from '../data/store';
import { createUndeclaredUiKeyWarner, DECLARED_UI_KEYS, STORE_UI_KEYS } from './uiKeys';
import { installUiOverrides, parseSceneUrl, useUiState } from './uiState';
import type { SceneUrl, UiValue } from './uiState';

export { useUiState };
export type { SceneUrl, UiValue };

const REAL_DATE = Date;

const IS_DEV = import.meta.env.DEV;

const warn = (message: string): void => {
  if (IS_DEV) console.warn(`[scene] ${message}`);
};

const EMPTY_SCENE_URL: SceneUrl = { after: null, clock: null, scene: null, ui: new Map() };

let url: SceneUrl = EMPTY_SCENE_URL;
let overrides: SceneOverrides = {};

const warnUndeclaredUiKeys = createUndeclaredUiKeyWarner(warn);

function install(search: string): void {
  url = parseSceneUrl(search);
  installUiOverrides(url.ui);
  installFrozenClock(url.clock);
  if (IS_DEV) warnUndeclaredUiKeys(url.ui.keys());
  overrides = storeOverrides(url.ui);
}

// Guarded rather than assumed: the URL is only a browser thing, and a module that
// throws when it is imported outside one is a module nothing can inspect. In the
// browser this body runs before every fixture's does.
if (typeof window !== 'undefined') {
  install(window.location.search);
}

/**
 * Freeze the wall clock at `?clock=`, for the life of the page.
 *
 * Patch, don't plumb: the mock reads the clock in six places (spec: *A frozen
 * clock* — `useNow`, the store's seeded handle, `metricSamples`, `volumeMocks`'s
 * `mtime`, `GuidelinesPage`'s module-scope stamps, `serviceMetricSamples`) and
 * every one of them already goes through `Date.now()` or a no-argument
 * `new Date()`. Patch those two primitives and no fixture and no screen needs an
 * edit at all.
 *
 * With no `?clock=` this is a no-op, so there is one code path rather than two:
 * the mock reads the real clock exactly as it does today.
 */
/**
 * An instant in epoch milliseconds, or null when the string does not parse.
 * Shared by the standalone `?clock=` install and by a frame's `clock` prop
 * (ticket 10), so both spellings of "frozen time" validate identically.
 */
export function parseClockInstant(iso: string): number | null {
  const instant = REAL_DATE.parse(iso);
  return Number.isNaN(instant) ? null : instant;
}

function installFrozenClock(iso: string | null): void {
  if (iso === null) return;

  const instant = parseClockInstant(iso);
  if (instant === null) {
    warn(`?clock=${iso} is not a parseable instant, so the mock keeps the wall clock.`);
    return;
  }

  patchDate(instant);
}

function patchDate(instant: number): void {
  type DateArgs = [] | [value: string | number | Date] | [year: number, monthIndex: number, ...rest: number[]];

  function FrozenDate(this: unknown, ...args: DateArgs): Date | string {
    // `Date()` as a plain call still hands back the fake now…
    if (!new.target) return new REAL_DATE(instant).toString();
    // …while a real argument (`new Date(row.updatedAt)`) is untouched.
    return args.length === 0 ? new REAL_DATE(instant) : (Reflect.construct(REAL_DATE, args) as Date);
  }

  // Keep the statics (`Date.parse`, `Date.UTC`) and the prototype identity
  // (`instanceof Date`, `Date.prototype`) by chaining to the real constructor.
  Object.setPrototypeOf(FrozenDate, REAL_DATE);
  FrozenDate.now = () => instant;
  FrozenDate.prototype = REAL_DATE.prototype;

  globalThis.Date = FrozenDate as unknown as DateConstructor;
}

/** The `?scene=` id that readiness echoes back, or null when the URL named none. */
export function sceneId(): string | null {
  return url.scene;
}

/**
 * `?after=`, parsed here and inert: ticket 02 registers the named adapters a
 * scene's post-action state is reached through, and this is the seam it reads.
 */
export function sceneAfter(): string | null {
  return url.after;
}

/**
 * Store-level state a scene seeds `PanelProvider` with, for the mount's lifetime.
 * No override means the store's own seed — which is what "back to normal" means
 * for storage state (spec: *Forcing state without a click*).
 */
export function sceneOverrides(): SceneOverrides {
  return overrides;
}

/**
 * The three storage-level things a scene can force, read from their declared
 * `store.*` keys. A scene names *which* volume ids are open and the store decides
 * what a handle looks like; the harness does not know how to build one.
 */
function storeOverrides(ui: ReadonlyMap<string, UiValue>): SceneOverrides {
  const forced: SceneOverrides = {};

  const speed = ui.get(STORE_UI_KEYS.speed);
  if (speed === 'instant' || speed === 'normal' || speed === 'slow') {
    forced.speed = speed;
  } else if (speed !== undefined) {
    warn(`${STORE_UI_KEYS.speed}=${String(speed)} is not one of instant, normal, slow.`);
  }

  const activity = ui.get(STORE_UI_KEYS.activity);
  if (activity === 'empty') {
    forced.activity = [];
  } else if (activity !== undefined) {
    warn(`${STORE_UI_KEYS.activity}=${String(activity)} is not a known activity seed; only 'empty' exists.`);
  }

  const handles = ui.get(STORE_UI_KEYS.handles);
  if (handles !== undefined) {
    forced.handles =
      typeof handles === 'string' && handles !== 'none' && handles !== ''
        ? handles
            .split(',')
            .map((volumeId) => volumeId.trim())
            .filter((volumeId) => volumeId !== '')
        : [];
  }

  return forced;
}
