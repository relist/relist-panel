/**
 * The scene registry's types — ticket 04's `Scene`, plus two declared additions.
 *
 * The type block is the ticket's own, verbatim in shape; the additions are the
 * two the ticket's Comments record:
 *
 *  - **`size`** — the engine (`src/storyboard/types.ts`, `BoardScene`) needs a
 *    natural size per frame. `width` comes from the scene's primary viewport
 *    (`VIEWPORT_SIZES[viewports[0]].width` — asserted by the registry test, so
 *    the number is never typed twice); `height` is a content estimate that only
 *    sizes the layout before the live frame self-measures.
 *  - **`ui` keys stay plain strings.** The registry deliberately does not import
 *    `SCREEN_UI_KEYS` and type its keys against it: ticket 02's batches land
 *    incrementally, and a compile-time coupling would make the registry and the
 *    swap land serially. The names are asserted mechanically by
 *    `scripts/registry.test.mjs` instead.
 *
 * Data-only by design: no runtime imports, so `node --test` can load this file.
 * The one type-level reference to the router (`ScenePaths`) is a `typeof
 * import(...)` — erased at runtime, never resolved.
 */

/** The three designed widths (spec: *Viewports, and what stays gated*). */
export type ViewportKey = 'desktop' | 'tablet' | 'mobile';

/**
 * The widths behind the keys, declared once and imported by the board — a scene
 * listing a width whose size lived only in the registry would be two sources of
 * truth for one number (ticket 04).
 */
export const VIEWPORT_SIZES: Record<ViewportKey, { width: number; height: number }> = {
  desktop: { width: 1600, height: 1000 },
  tablet: { width: 834, height: 1112 },
  mobile: { width: 390, height: 844 },
} as const;

/** A scene's menu group — the column its frame lands in. Order lives in `groups.ts`. */
export type SceneGroupId =
  | 'overview'
  | 'projects'
  | 'monitoring'
  | 'project'
  | 'compose'
  | 'volume-explorer'
  | 'design-system'
  | 'auth'
  | 'feedback';

/** What a frame is showing: a screen, an overlay beside it, or a feedback note. */
export type SceneKind = 'screen' | 'overlay' | 'feedback';

/**
 * A declared connector. `nav` is derived from `menuPath` by the board and never
 * hand-written; `opens` is a trigger on the parent screen's box (`at` is a
 * fraction of that parent frame, how "the ⋯ button in the header" is
 * expressible without pixel-picking); `tab` is an in-frame tab bar entry.
 * Overlay scenes carry their own `opens` edge — the link *from their screen* —
 * so a scene entry reads as the whole truth about its frame.
 */
export type SceneLink = {
  kind: 'nav' | 'opens' | 'tab';
  from: { scene: string; at?: [number, number] };
  to: string;
  label?: string;
};

/** The router's `paths` object, type-only — the only way a `hash` is built. */
export type ScenePaths = typeof import('../app/router').paths;

/**
 * One scene: a route plus a forced state — one frame on the board. Nothing is
 * rendered (05's successor) or laid out (06) that is not declared here first.
 */
export type Scene = {
  /** Stable slug: `'compose-logs-fatal'`. Unique, kebab-case; feedback quotes it. */
  id: string;
  /** `'Compose — Logs drawer'`. */
  title: string;
  /** Menu group: the column the frame lands in. */
  group: SceneGroupId;
  /** A `screen` renders the shell, an `overlay` is an overlay on a screen, a `feedback` state has no home screen. */
  kind: SceneKind;
  /** Which widths this scene is rendered at. Desktop-only until ticket 03 lands. */
  viewports: ViewportKey[];
  /** A route built through `app/router.ts`'s `paths` — never a literal, never a `ui` key (D5: tabs are part of the hash). */
  hash: string;
  /** Forced state, keys from `app/uiKeys.ts` (screen keys, key families, `store.*` keys) — plain strings, asserted by test. */
  ui?: Record<string, unknown>;
  /** A named transition adapter (`app/sceneAdapters.ts`): `toast:…`, `submit:…`, `seed:…`. */
  after?: string;
  /** One line: what to look at, and why the state is the way it is. Restating the title is a bug in the caption. */
  caption: string;
  /** `['Projects', 'my-saas', 'production', 'web']` — order + flow; the board derives its `nav` lines from it. */
  menuPath: string[];
  /**
   * Natural (untransformed) size of the frame. `width` is the primary viewport's
   * width; `height` is a content estimate — live frames self-measure, the
   * estimate only sizes the layout (ticket 04 Comments).
   */
  size: { width: number; height: number };
  /**
   * Declared connectors: overlays carry the `opens` edge from the screen they
   * belong to; screens carry `tab` edges to the pane scenes their tabs show.
   */
  links?: SceneLink[];
};

/**
 * One group module's export. A function of `paths` rather than a bare array so
 * every `hash` is built through the router at call time, while the module
 * itself stays free of runtime imports — which is what lets
 * `scripts/registry.test.mjs` load it under plain Node.
 */
export type SceneGroupModule = {
  /** Same id as the `GROUPS` entry this module belongs to. */
  id: SceneGroupId;
  /** The group's scenes, in review order — the board stacks them in this order. */
  scenes: (paths: ScenePaths) => Scene[];
};
