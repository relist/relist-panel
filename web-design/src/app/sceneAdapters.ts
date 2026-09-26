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
import { useEffect } from 'react';

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

  useEffect(() => {
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
