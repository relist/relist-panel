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

/**
 * Screen keys: one per `useUiState` call site. Ticket 02 fills this in —
 * batch 1 below; batches 2 (`ProjectsPage`, `ProjectPage`, `VariablesPanel`)
 * and 3 (`ComposePage`, `VolumeExplorerPage`) append theirs.
 *
 * A trailing `.*` declares a *family* (ticket 02, decision D2): the state is
 * one instance per row, addressed by naming the row in the key —
 * `?ui=fullscreen.revealed.DATABASE_URL=1`. The parse already allows it (a
 * value splits on its first `=`; the key is whatever came before), so the
 * declaration only keeps the warner quiet for a legitimate addressed key
 * while a key matching no declared family still warns.
 */
export const SCREEN_UI_KEYS: readonly string[] = [
  // app/AppShell.tsx
  'shell.paletteOpen',
  'shell.refreshing',
  // pages/OverviewPage.tsx
  'overview.refreshing',
  // pages/MonitoringPage.tsx
  'monitoring.window',
  // components/ServiceMonitor.tsx
  'monitor.service',
  'monitor.window',
  // pages/AuthPages.tsx
  'auth.submitting',
  'auth.setupStep',
  // pages/StatesGalleryPage.tsx
  'states.dangerOpen',
  'states.confirmOpen',
  'states.section',
  // components/MetricChart.tsx
  'chart.hoverAt',
  // components/ComposeVolumesTab.tsx
  'volumes.error',
  'volumes.starting',
  // components/FullscreenEditor.tsx — `revealed` is one instance per variable
  // row (family, D2); `railQuery` is the rail's single filter field.
  'fullscreen.revealed.*',
  'fullscreen.railQuery',
  // components/DangerModal.tsx
  'danger.typed',
  // components/CommandPalette.tsx
  'palette.query',
  // components/StartHandleDialog.tsx
  'startHandle.mode',
  'startHandle.uid',
  'startHandle.gid',
  // Ticket 02 integration (batch-1 gaps): the account `Dropdown` has no state
  // of its own to swap, so the menu is forced through its own controlled
  // `open` (audit §2, second table).
  'shell.accountMenu',
  // The Overview attention fiction: the attention list is *derived* from the
  // store's composes, so the all-clear scene has no boolean to force — this
  // key is what the derivations consult instead of their computation (the
  // OverviewPage list, and AppShell's refresh badge, which counts the same
  // Composes). The `seed:overview.allClear` adapter drives the same state
  // through the owning setters, so the registry's `after:`-named scene needs
  // no registry edit.
  'overview.attentionClear',
  // pages/ProjectsPage.tsx — ticket 02, batch 2. `editing`/`deleting` hold a
  // `Project` (D1: seeded only through the `object:` adapters, never a URL
  // literal); `rowMenu` is the per-row ⋯ menu family (D2).
  'projects.query',
  'projects.sort',
  'projects.view',
  'projects.modalOpen',
  'projects.editing',
  'projects.deleting',
  'projects.rowMenu.*',
  // pages/ProjectPage.tsx — batch 2. `composeYAML` is a whole YAML document
  // (§5.10: reached through `fixture:project.composeYAML`, not a URL literal)
  // and `deletingCompose` holds a `Compose` (D1, via
  // `object:project.deletingCompose`); `rowMenu`/`deleteEnv` are the per-row
  // menu and delete-Popconfirm families (D2).
  'project.environmentModalOpen',
  'project.composeModalOpen',
  'project.composeYAML',
  'project.deletingCompose',
  'project.deleteProjectOpen',
  'project.rowMenu.*',
  'project.deleteEnv.*',
  // components/VariablesPanel.tsx — batch 2. `revealed` and `deletePopconfirm`
  // are one instance per variable row (families, D2, audit §5.5); `editing`
  // holds an `EnvironmentVariable` (D1) and `bulkText` is the paste-a-`.env`
  // document (§5.10, via the `fixture:variables.bulk*` adapters).
  'variables.query',
  'variables.editing',
  'variables.modalOpen',
  'variables.bulkOpen',
  'variables.bulkText',
  'variables.revealed.*',
  'variables.deletePopconfirm.*',
  // pages/ComposePage.tsx — ticket 02, batch 3. `draft` is a whole YAML
  // document whose mount reset keeps running (D3's carve-out), so the dirty
  // state is delivered by the `fixture:compose.draft` adapter (§5.10), not a
  // URL literal; `logServices` is an array — a forced value is the one service
  // name the URL carried, normalized by the screen (D1); `moreOpen` is the
  // header ⋯ menu forced through its controlled `open` (audit §2).
  'compose.draft',
  'compose.fullscreenOpen',
  'compose.logsOpen',
  'compose.logsLoading',
  'compose.logServices',
  'compose.logLines',
  'compose.deleteOpen',
  'compose.moreOpen',
  // pages/VolumeExplorerPage.tsx — batch 3. `editing` holds
  // `{contents, path}` and `editorTooLarge` a `VolumeEntry` (D1: seeded only
  // through the `object:volume.*` adapters, via `openFile`); `rowMenu` and
  // `deletePopconfirm` are one instance per entry row (families, D2, §5.5).
  'volume.resolutionError',
  'volume.browseError',
  'volume.startOpen',
  'volume.editing',
  'volume.editorTooLarge',
  'volume.uploadError',
  'volume.downloadOpen',
  'volume.newEntry',
  'volume.newEntryName',
  'volume.expiredHere',
  'volume.archiveFormat',
  'volume.rowMenu.*',
  'volume.deletePopconfirm.*',
];

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
  // A declared `name.*` family covers every addressed key under `name.`
  // (ticket 02, decision D2); anything else is an exact match.
  const families = declared
    .filter((entry) => entry.endsWith('.*'))
    .map((entry) => entry.slice(0, -2));
  return [...new Set(keys)].filter((key) => {
    if (known.has(key)) return false;
    return !families.some((prefix) => key.startsWith(prefix));
  });
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
