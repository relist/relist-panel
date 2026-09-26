/**
 * The named `after:` transition adapters — the registry, and nothing more.
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
