/**
 * The named `after:` transition adapters — the registry, and the two seams an
 * adapter reaches the running app through (the `<AntApp>` handles and the
 * screen-published state below). Nothing else lives here.
 *
 * A scene's post-action state (a toast fired, a form left submitted) is reached
 * through a *named* transition: `?after=deployed` standalone, or the `after`
 * prop on a `SceneScopeProvider` in a frame. The names are registered here by
 * ticket 02's batches; the harness and the board only ever hold the name.
 *
 * An adapter runs inside the frame that named it, in a mount effect — plain,
 * synchronous, and idempotent under `StrictMode`'s double effects (the scope
 * guards, not the adapter).
 */
import { App as AntApp } from 'antd';
import { useLayoutEffect } from 'react';

/**
 * What an adapter may do when it runs. Deliberately a bare procedure: it
 * reaches the world through the module seams (`navigate`, the registry's own
 * closures) rather than through arguments, because it is registered once at
 * module scope and must name a transition, not a frame.
 */
export type SceneAdapter = () => void;

const IS_DEV = import.meta.env.DEV;

const registry = new Map<string, SceneAdapter>();

/** Name a transition. Re-registering the same name with the same body is a no-op. */
export function registerSceneAdapter(name: string, adapter: SceneAdapter): void {
  if (IS_DEV && registry.has(name) && registry.get(name) !== adapter) {
    console.warn(`[sceneAdapters] "${name}" was already registered with a different body — the new one wins.`);
  }
  registry.set(name, adapter);
}

/**
 * Look a transition up by name. An unknown name is a scene typo — a silent
 * no-op scene would photograph or render fine and simply not be the state it
 * claims — so in dev it is loud: the frame that named it crashes into its own
 * error boundary with the message.
 */
export function getSceneAdapter(name: string): SceneAdapter | undefined {
  const adapter = registry.get(name);
  if (adapter === undefined && IS_DEV) {
    throw new Error(`[sceneAdapters] unknown adapter "${name}" — nothing registered it in app/sceneAdapters.ts.`);
  }
  return adapter;
}

/**
 * How an adapter reaches the app it runs in.
 *
 * An adapter is a plain module-scope function — it cannot call `App.useApp()`,
 * even though it always *runs* inside an `<AntApp>` (a frame's own nested one,
 * or the root one standalone). The bridge is the one-way hand-off: a screen
 * that owns adapters mounts `<SceneAdapterBridge />`, and the adapter reads
 * the app's own `message`/`notification` handles through it — so the copy is
 * the app's own, and inside a frame the toast lands in that frame.
 */
type SceneAppApi = ReturnType<typeof AntApp.useApp>;

let appApi: SceneAppApi | undefined;
let bridges = 0;

/** Renders nothing; publish the mounting `<AntApp>`'s handles to the adapters. */
export function SceneAdapterBridge(): null {
  const app = AntApp.useApp();

  useLayoutEffect(() => {
    appApi = app;
    bridges += 1;
    return () => {
      bridges -= 1;
      if (bridges === 0) appApi = undefined;
    };
  }, [app]);

  return null;
}

/**
 * State a screen publishes for its adapters: a `Form` instance, a setter
 * recipe — anything an object-valued forced state needs (ticket 02, decision
 * D1: a `?ui=` string stays scalar, so an object is seeded through the app's
 * own setter instead). The unpublisher keeps a screen's handles from
 * outliving it.
 */
const handles = new Map<string, unknown>();

export function publishSceneHandle<T>(name: string, value: T): () => void {
  handles.set(name, value);
  return () => {
    if (handles.get(name) === value) handles.delete(name);
  };
}

function takeHandle<T>(name: string): T | undefined {
  return handles.get(name) as T | undefined;
}

function runHandle<T>(adapter: string, name: string, run: (value: T) => void): void {
  const value = takeHandle<T>(name);
  if (value === undefined) {
    if (IS_DEV) console.warn(`[sceneAdapters] "${adapter}" found no "${name}" handle — the screen that publishes it is not mounted.`);
    return;
  }
  run(value);
}

function sceneApp(adapter: string): SceneAppApi | undefined {
  if (appApi === undefined && IS_DEV) {
    console.warn(`[sceneAdapters] "${adapter}" ran with no <SceneAdapterBridge /> mounted — no antd App api to reach.`);
  }
  return appApi;
}

/** The slice of antd's `FormInstance` the submit adapters need. */
export type SceneForm = {
  setFieldsValue: (values: Record<string, unknown>) => void;
  submit: () => void;
};

/*
 * ---------------------------------------------------------------------------
 * Batch 1 registrations (ticket 02). The names follow the audit's §3 table.
 * Every adapter is idempotent by construction: a toast is keyed, so a second
 * call replaces the same notice instead of stacking one; an object seed goes
 * through the owning setter, which holds its current value; a submit replays
 * onto the same form and lands in the same state.
 * ---------------------------------------------------------------------------
 */

// pages/StatesGalleryPage.tsx — the five outcome toasts and the keyed
// deploy notification, sticky (`duration: 0`) so a frame can be reviewed
// mid-toast. Copy is verbatim from the screen's own handlers.
registerSceneAdapter('toast:states.submitted', () => {
  sceneApp('toast:states.submitted')?.message.success({ content: 'Submitted', duration: 0, key: 'toast:states.submitted' });
});

registerSceneAdapter('toast:states.deployed', () => {
  sceneApp('toast:states.deployed')?.message.success({ content: 'Deployed web', duration: 0, key: 'toast:states.deployed' });
});

registerSceneAdapter('toast:states.error', () => {
  sceneApp('toast:states.error')?.message.error({
    content: 'Could not stop worker: container is restarting',
    duration: 0,
    key: 'toast:states.error',
  });
});

registerSceneAdapter('toast:states.info', () => {
  sceneApp('toast:states.info')?.message.info({
    content: 'Status refreshed from the host',
    duration: 0,
    key: 'toast:states.info',
  });
});

registerSceneAdapter('toast:states.warning', () => {
  sceneApp('toast:states.warning')?.message.warning({
    content: 'Deploy is still running — the YAML you saved is not live yet',
    duration: 0,
    key: 'toast:states.warning',
  });
});

// The screen's long-running action is a keyed notification that swaps
// progress for result; a frame holds the "deploying" half, sticky.
registerSceneAdapter('toast:states.deployNotification', () => {
  sceneApp('toast:states.deployNotification')?.notification.info({
    description: 'Writing files, joining the shared network, bringing containers up.',
    duration: 0,
    key: 'deploy-demo',
    message: 'Deploying my-saas / production / web',
    placement: 'bottomRight',
  });
});

// pages/AuthPages.tsx — the post-submit states (audit §3b). `form.submit()`
// runs the forms' own validation and handlers, never a fake error object.
registerSceneAdapter('submit:auth.signin.valid', () => {
  runHandle<SceneForm>('submit:auth.signin.valid', 'auth.signin.form', (form) => {
    form.setFieldsValue({ email: 'admin@relist.local', password: 'relist-admin' });
    form.submit();
  });
});

registerSceneAdapter('submit:auth.signin.empty', () => {
  runHandle<SceneForm>('submit:auth.signin.empty', 'auth.signin.form', (form) => {
    form.setFieldsValue({ email: '', password: '' });
    form.submit();
  });
});

// The one genuinely unreachable state today: the confirm field disagreeing
// with the password, refused by the form's own validator.
registerSceneAdapter('submit:auth.setup.mismatchedConfirm', () => {
  runHandle<SceneForm>('submit:auth.setup.mismatchedConfirm', 'auth.setup.form', (form) => {
    form.setFieldsValue({ confirmPassword: 'relist-admin-2', email: 'admin@relist.local', password: 'relist-admin-1' });
    form.submit();
  });
});

registerSceneAdapter('toast:auth.adminCreated', () => {
  sceneApp('toast:auth.adminCreated')?.message.success({
    content: 'Admin account created',
    duration: 0,
    key: 'toast:auth.adminCreated',
  });
});

// components/ComposeVolumesTab.tsx — `volumes.starting` holds a `Volume`
// (ticket 02, decision D1): a scene seeds it through the tab's own setter,
// with the first Volume the tab itself resolved from the store.
registerSceneAdapter('object:volumes.starting', () => {
  runHandle<() => void>('object:volumes.starting', 'volumes.starting.seed', (seed) => {
    seed();
  });
});

/*
 * ---------------------------------------------------------------------------
 * Integration additions (ticket 02's batch-1 gaps, run alongside batches 2/3).
 * The names are the ones ticket 04's registry already declares for these
 * scenes; each recipe goes through the owning screen's own setter, published
 * from a layout effect so it is in place before the scoped AfterRunner's
 * passive effect runs. Idempotent: a second call re-pins the state it holds.
 * ---------------------------------------------------------------------------
 */

// pages/MonitoringPage.tsx and components/ServiceMonitor.tsx — the D4 loading
// scenes. The screens' mount loads keep running (D3), so the recipe pins the
// fetch's own loading state instead of forcing a boolean: the skeleton stays
// up when the fetch answers, and the frame is reviewed mid-load however fast
// the mock answers.
registerSceneAdapter('monitoring:loading', () => {
  runHandle<() => void>('monitoring:loading', 'monitoring.loading.hold', (hold) => {
    hold();
  });
});

registerSceneAdapter('monitor:loading', () => {
  runHandle<() => void>('monitor:loading', 'monitor.loading.hold', (hold) => {
    hold();
  });
});

// pages/OverviewPage.tsx (+ app/AppShell.tsx) — the all-clear scene. Attention
// is derived from the store's composes, so the scene seeds the derivations
// through their own setters rather than through a fixture: the Overview list
// and the shell's refresh badge both read `overview.attentionClear`. Either
// copy may be absent (a bare route renders no shell), so each seed warns for
// itself and the other still lands.
registerSceneAdapter('seed:overview.allClear', () => {
  runHandle<() => void>('seed:overview.allClear', 'overview.attention.seed.page', (seed) => {
    seed();
  });
  runHandle<() => void>('seed:overview.allClear', 'overview.attention.seed.shell', (seed) => {
    seed();
  });
});

/*
 * ---------------------------------------------------------------------------
 * Batch 2 registrations (ticket 02): pages/ProjectsPage.tsx,
 * pages/ProjectPage.tsx, components/VariablesPanel.tsx. Same contract as the
 * blocks above; the screens publish the handles these run. The names follow
 * ticket 04's grammar (`submit:<screen>.<surface>.<variant>`, `object:<uiKey>`,
 * `fixture:<name>`), except `projects:empty`, which is the name the registry's
 * own scene carries.
 * ---------------------------------------------------------------------------
 */

// pages/ProjectsPage.tsx — the New Project modal's own validation, run on the
// published form with the name empty (audit §3b): "Enter a Project name" under
// the field, the modal still open, nothing lost.
registerSceneAdapter('submit:projects.new.empty', () => {
  runHandle<SceneForm>('submit:projects.new.empty', 'projects.new.form', (form) => {
    form.setFieldsValue({ description: '', name: '' });
    form.submit();
  });
});

// `projects.editing` holds a `Project` (decision D1): seeded through the
// screen's own openEdit, which prefills the form and opens the modal — the
// whole card-menu — Edit — transition, never a synthetic object.
registerSceneAdapter('object:projects.editing', () => {
  runHandle<() => void>('object:projects.editing', 'projects.editing.seed', (seed) => {
    seed();
  });
});

// `projects.deleting` holds a `Project` (D1): the typed-name confirm opens
// through the screen's own setter, so the false→true transition resets
// `danger.typed` and OK starts disabled — the state the caption reviews. A
// scene that wants the typed moment forces `danger.typed` beside the adapter.
registerSceneAdapter('object:projects.deleting', () => {
  runHandle<() => void>('object:projects.deleting', 'projects.deleting.seed', (seed) => {
    seed();
  });
});

// The typed-name flow completed (audit §3b): the confirm opens seeded and the
// screen's real onConfirm runs — the row disappears and the app's own
// "Deleted …" toast fires through the frame's nested AntApp.
registerSceneAdapter('submit:projects.delete.confirmed', () => {
  runHandle<() => void>('submit:projects.delete.confirmed', 'projects.delete.flow', (flow) => {
    flow();
  });
});

// First-run (scene `projects-empty`): every seeded Project removed through the
// store's own deleteProject, so readiness waits for the deletes to settle and
// the empty state is what the store actually produces.
registerSceneAdapter('projects:empty', () => {
  runHandle<() => void>('projects:empty', 'projects.empty.seed', (seed) => {
    seed();
  });
});

// pages/ProjectPage.tsx — the New Environment modal's own validation, run with
// the name empty: "Enter an Environment name" under the field.
registerSceneAdapter('submit:project.environment.empty', () => {
  runHandle<SceneForm>('submit:project.environment.empty', 'project.environment.form', (form) => {
    form.setFieldsValue({ name: '' });
    form.submit();
  });
});

// The New Compose modal's own pattern rule, run with a name no compose may
// have: the required rule passes, so the frame shows exactly
// "Use letters, digits, dashes and underscores".
registerSceneAdapter('submit:project.compose.badName', () => {
  runHandle<SceneForm>('submit:project.compose.badName', 'project.compose.form', (form) => {
    form.setFieldsValue({ name: 'has a space' });
    form.submit();
  });
});

// Settings saved (audit §3b): the screen's own effect prefills the form from
// the project, so a submit is the real update — the store's save runs and the
// app's "Saved Project details" toast fires through the frame.
registerSceneAdapter('submit:project.settings.saved', () => {
  runHandle<SceneForm>('submit:project.settings.saved', 'project.settings.form', (form) => {
    form.submit();
  });
});

// §5.10: `project.composeYAML` is a whole document — seeded as the screen's own
// "Insert an example" hands STARTER_YAML over, which is what enables the OK.
registerSceneAdapter('fixture:project.composeYAML', () => {
  runHandle<() => void>('fixture:project.composeYAML', 'project.composeYAML.fixture', (seed) => {
    seed();
  });
});

// `project.deletingCompose` holds a `Compose` (D1): seeded with web through the
// screen's own setter, per the registry's caption; the open transition resets
// `danger.typed`, so OK starts disabled. The typed moment is `danger.typed`.
registerSceneAdapter('object:project.deletingCompose', () => {
  runHandle<() => void>('object:project.deletingCompose', 'project.deletingCompose.seed', (seed) => {
    seed();
  });
});

// The typed-name flow completed (audit §3b): the confirm opens seeded with web
// and the screen's real onConfirm runs — the store deletes, the toast fires.
registerSceneAdapter('submit:project.deleteCompose.confirmed', () => {
  runHandle<() => void>('submit:project.deleteCompose.confirmed', 'project.deleteCompose.flow', (flow) => {
    flow();
  });
});

// Scene `project-zero-envs`: the project named by the scene's hash (blog) has
// one Environment, and a Project with none is exactly its absence — removed
// through the store's own deleteEnvironment, readiness waiting for it.
registerSceneAdapter('seed:project.zeroEnvs', () => {
  runHandle<() => void>('seed:project.zeroEnvs', 'project.zeroEnvs.seed', (seed) => {
    seed();
  });
});

// components/VariablesPanel.tsx — `variables.editing` holds an
// `EnvironmentVariable` (D1): seeded through the panel's own openEdit with
// DATABASE_URL, which prefills the form and opens the modal.
registerSceneAdapter('object:variables.editing', () => {
  runHandle<() => void>('object:variables.editing', 'variables.editing.seed', (seed) => {
    seed();
  });
});

// The paste is the state (audit §3b): the fixture `.env` arrives through the
// panel's own setter, so the parsed preview, the duplicate check and the OK's
// "Replace N variables with M" are all the real derivation's output.
registerSceneAdapter('fixture:variables.bulkText', () => {
  runHandle<() => void>('fixture:variables.bulkText', 'variables.bulkText.fixture', (seed) => {
    seed();
  });
});

// The duplicate-key refusal: the same fixture with one key repeated, so the
// error names it and the real `okButtonProps` derivation disables the OK.
registerSceneAdapter('fixture:variables.bulkDuplicate', () => {
  runHandle<() => void>('fixture:variables.bulkDuplicate', 'variables.bulkDuplicate.fixture', (seed) => {
    seed();
  });
});

/*
 * ---------------------------------------------------------------------------
 * Batch 3 registrations (ticket 02): pages/ComposePage.tsx and
 * pages/VolumeExplorerPage.tsx — the last of the swap. Same contract as the
 * blocks above, and the same grammar; every handle these run is published by
 * the owning screen from a *layout* effect, so it exists before the scoped
 * AfterRunner's passive effect fires (an ordinary effect publishes one flush
 * too late).
 * ---------------------------------------------------------------------------
 */

// pages/ComposePage.tsx — the dirty editor (audit §5.10): the mount reset on
// `compose.draft` keeps running (decision D3's carve-out: it is a data load,
// not an overlay reset), so the seed defers past it and lands the offending
// draft through the app's own setter.
registerSceneAdapter('fixture:compose.draft', () => {
  runHandle<() => void>('fixture:compose.draft', 'compose.draft.seed', (seed) => {
    seed();
  });
});

// The Volumes tab's resolution, held: the tab's own state is excluded as data,
// so the hold is a store lever (`holdVolumeResolution`) armed on the tab's
// behalf by its page, which shares the frame's PanelProvider. Deliberately
// outside `track`, so the frame still becomes ready while it holds.
registerSceneAdapter('volumes:loading', () => {
  runHandle<() => void>('volumes:loading', 'volumes.loading.hold', (hold) => {
    hold();
  });
});

// pages/VolumeExplorerPage.tsx — the held directory listing, via the store's
// `holdVolumeListing` on this page's Volume id. Resolution still lands, so the
// skeleton keeps the header and the Volume fact.
registerSceneAdapter('volume:loading', () => {
  runHandle<() => void>('volume:loading', 'volume.loading.hold', (hold) => {
    hold();
  });
});

// The read-only handle: the store's seed hardcodes rw and lives on uploads, so
// the scene's handle is re-minted (or re-moded) read-only through the store's
// own `seedHandleMode` — the shape stays the store's, never a painted flag.
registerSceneAdapter('seed:volume.handleRo', () => {
  runHandle<() => void>('seed:volume.handleRo', 'volume.handle.seedRo', (seed) => {
    seed();
  });
});

// `volume.editing` and `volume.editorTooLarge` hold objects (decision D1) and
// are reached through the screen's own `openFile` — its size check is what
// refuses the too-large file — with the named fixture entry.
registerSceneAdapter('object:volume.editing', () => {
  runHandle<(name: string) => void>('object:volume.editing', 'volume.openFile.seed', (open) => {
    open('README.txt');
  });
});

registerSceneAdapter('object:volume.editorTooLarge', () => {
  runHandle<(name: string) => void>('object:volume.editorTooLarge', 'volume.openFile.seed', (open) => {
    open('export.csv');
  });
});

// The upload refusals, reached through the screen's own `upload` so both
// alerts carry the app's own copy: the first past the 1 GiB upload cap, the
// second under it but past the uploads Volume's declared free space.
registerSceneAdapter('seed:volume.uploadTooLarge', () => {
  runHandle<(file: { name: string; size: number }) => void>('seed:volume.uploadTooLarge', 'volume.upload.seed', (upload) => {
    upload({ name: 'postgres-backup.tar', size: 1_610_612_736 });
  });
});

registerSceneAdapter('seed:volume.uploadNoSpace', () => {
  runHandle<(file: { name: string; size: number }) => void>('seed:volume.uploadNoSpace', 'volume.upload.seed', (upload) => {
    upload({ name: 'bundle.tar', size: 4_194_304 });
  });
});

// Audit §3b — the post-action surfaces of these two screens. No registry scene
// names these yet (ticket 04's list carries none for them), but the grammar is
// completed here so a follow-up scene only names the adapter. Each closure is
// handed the values the real handler would read from state.
registerSceneAdapter('submit:compose.delete.typed', () => {
  runHandle<() => void>('submit:compose.delete.typed', 'compose.delete.confirm', (confirm) => {
    confirm();
  });
});

registerSceneAdapter('submit:compose.editor.save', () => {
  runHandle<(next: string) => void>('submit:compose.editor.save', 'compose.draft.save', (save) => {
    save('services:\n  api:\n    image: ghcr.io/relist/my-saas-api:2.15.0\n');
  });
});

registerSceneAdapter('submit:volume.newEntry.create', () => {
  runHandle<(kind: 'dir' | 'file', name: string) => void>('submit:volume.newEntry.create', 'volume.newEntry.submit', (create) => {
    create('file', 'NOTES.txt');
  });
});

registerSceneAdapter('submit:volume.archive.download', () => {
  runHandle<(format: 'tar.gz' | 'zip') => void>('submit:volume.archive.download', 'volume.archive.confirm', (confirm) => {
    confirm('tar.gz');
  });
});
