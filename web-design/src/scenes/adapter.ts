/**
 * The adapter between the app's scene registry (ticket 04) and the storyboard
 * engine's data contract (`src/storyboard/types.ts`). This is the one place
 * that knows both shapes — the engine stays standalone, the registry stays
 * data-only, and every mapping decision lives here where it can be read as a
 * whole.
 *
 * The three decisions:
 *
 *  - **`parentId` is derived, never declared.** The registry hangs an `opens`
 *    link on the *overlay* scene (`from` the screen that opens it, `to` the
 *    overlay), so a scene entry reads as the whole truth about its frame; the
 *    engine hangs the trigger on the *parent* frame (`BoardScene.opens`). The
 *    adapter inverts the declaration: every `opens` link pointing at a scene
 *    becomes an entry on the `from` scene's `opens` array, and the first such
 *    link's source becomes the scene's `parentId` (the overlay rail). A second,
 *    different-parent trigger would still draw its dashed line but not move the
 *    frame — a frame sits in one rail.
 *  - **`tabs` come from the scene's own links.** The registry declares tab
 *    edges on the screen whose tab bar shows the pane (`from` itself), which is
 *    already the engine's direction.
 *  - **The menu mirrors the sider's top-level entries** (`AppShell.tsx`):
 *    Overview, Projects, Monitoring, the Recent Projects group (which lands on
 *    the drill-down `project` column) and Design system — plus Auth and
 *    Feedback, whose columns the registry declares and the sider has no entry
 *    for. Compose and Volume Explorer get no menu item on purpose: the sider
 *    has none either (they are reached by drilling into a project), and a nav
 *    line that does not exist in the app would say something untrue. Their
 *    columns are how the flow layer shows exactly that.
 *
 * `ui` values are coerced through the harness's own rule (`coerceUiValue`), so
 * a board frame's forced state is byte-identical to its *Open live* URL's.
 */

import { coerceUiValue } from '../app/uiState';
import type { UiValue } from '../app/uiState';
import { STORE_UI_KEYS } from '../app/uiKeys';
import type { SceneOverrides } from '../data/store';
import type { BoardGroup, BoardMenuItem, BoardOpensLink, BoardScene, BoardTabLink } from '../storyboard';

import { GROUPS } from './groups';
import type { Scene } from './types';

/** The board's columns, in menu order — `GROUPS` is the single source of it. */
export const boardGroups: BoardGroup[] = GROUPS.map(({ id, label }) => ({ id, label }));

/**
 * The inert menu miniature's items — the sider's own labels (AppShell.tsx),
 * each drawing one solid nav line to the column it leads to.
 */
export const boardMenu: BoardMenuItem[] = [
  { id: 'menu-overview', label: 'Overview', targetGroupId: 'overview' },
  { id: 'menu-projects', label: 'Projects', targetGroupId: 'projects' },
  { id: 'menu-monitoring', label: 'Monitoring', targetGroupId: 'monitoring' },
  { id: 'menu-recent-projects', label: 'Recent Projects', targetGroupId: 'project' },
  { id: 'menu-design-system', label: 'Design system', targetGroupId: 'design-system' },
  { id: 'menu-auth', label: 'Auth', targetGroupId: 'auth' },
  { id: 'menu-feedback', label: 'Feedback', targetGroupId: 'feedback' },
];

/** A registry `ui` value, coerced the way the URL harness would coerce it. */
export function toUiValue(value: unknown): UiValue {
  return typeof value === 'boolean' ? value : coerceUiValue(String(value));
}

/** The `?ui=` spelling of a coerced value, for an *Open live* URL. */
export function serializeUiValue(value: UiValue): string {
  if (value === true) return '1';
  if (value === false) return '0';
  return value;
}

/**
 * A frame's store-level seed, from its coerced `ui` map — the same three
 * `store.*` keys the standalone harness (`app/scene.ts`'s `storeOverrides`)
 * turns into `PanelProvider` props. Rebuilt here because the module function
 * reads the URL once for the whole page, while a frame needs its own scene's.
 */
export function sceneOverridesOf(ui: ReadonlyMap<string, UiValue>): SceneOverrides {
  const overrides: SceneOverrides = {};

  const speed = ui.get(STORE_UI_KEYS.speed);
  if (speed === 'instant' || speed === 'normal' || speed === 'slow') {
    overrides.speed = speed;
  }

  const activity = ui.get(STORE_UI_KEYS.activity);
  if (activity === 'empty') {
    overrides.activity = [];
  }

  const handles = ui.get(STORE_UI_KEYS.handles);
  if (handles !== undefined) {
    overrides.handles =
      typeof handles === 'string' && handles !== 'none' && handles !== ''
        ? handles
            .split(',')
            .map((volumeId) => volumeId.trim())
            .filter((volumeId) => volumeId !== '')
        : [];
  }

  return overrides;
}

/** Registry scenes in, engine frames out. Pure: same input, same output. */
export function toBoardScenes(scenes: readonly Scene[]): BoardScene[] {
  // Invert the registry's `opens` convention: the link lives on the overlay
  // and names its parent; the engine wants the trigger on the parent.
  const opensBySource = new Map<string, BoardOpensLink[]>();
  const parentOf = new Map<string, string>();

  for (const scene of scenes) {
    for (const link of scene.links ?? []) {
      if (link.kind !== 'opens') continue;
      let list = opensBySource.get(link.from.scene);
      if (list === undefined) {
        list = [];
        opensBySource.set(link.from.scene, list);
      }
      // The first trigger wins the rail; later ones only draw their line.
      if (list.length === 0) parentOf.set(scene.id, link.from.scene);
      list.push({
        anchor: link.from.at ?? [0.5, 0.5],
        targetId: scene.id,
        ...(link.label !== undefined ? { label: link.label } : {}),
      });
    }
  }

  // Tab ownership, cycle-safe. The engine lets the first declarer of a target
  // own it, but the project screens declare their tab bars *bidirectionally*
  // (Composes ↔ Variables ↔ Settings), which without this pass forms a tab
  // cycle: no scene in the cycle is a `top` frame, and the engine's placement
  // silently drops the whole component (six frames, their rails and their
  // edges with it). The adapter owns each target exactly once — first
  // declaration wins, a declaration that would close a cycle is dropped — so
  // the tabs stay a forest and every frame places.
  const tabOwner = new Map<string, string>();
  const tabChildren = new Map<string, string[]>();
  const closesCycle = (sourceId: string, targetId: string): boolean => {
    const stack = [targetId];
    const seen = new Set<string>();
    while (stack.length > 0) {
      const current = stack.pop();
      if (current === sourceId) return true;
      if (current === undefined || seen.has(current)) continue;
      seen.add(current);
      stack.push(...(tabChildren.get(current) ?? []));
    }
    return false;
  };

  for (const scene of scenes) {
    for (const link of scene.links ?? []) {
      if (link.kind !== 'tab' || tabOwner.has(link.to) || closesCycle(scene.id, link.to)) continue;
      tabOwner.set(link.to, scene.id);
      let children = tabChildren.get(scene.id);
      if (children === undefined) {
        children = [];
        tabChildren.set(scene.id, children);
      }
      children.push(link.to);
    }
  }

  const titleOf = new Map(scenes.map((scene) => [scene.id, scene.title]));

  return scenes.map((scene) => {
    const opens = opensBySource.get(scene.id);
    const tabs: BoardTabLink[] = (scene.links ?? [])
      .filter((link) => link.kind === 'tab' && tabOwner.get(link.to) === scene.id)
      .map((link) => ({ label: link.label ?? titleOf.get(link.to) ?? link.to, targetId: link.to }));
    const parentId = parentOf.get(scene.id);

    return {
      id: scene.id,
      title: scene.title,
      groupId: scene.group,
      kind: scene.kind,
      size: scene.size,
      caption: scene.caption,
      ...(parentId !== undefined ? { parentId } : {}),
      ...(opens !== undefined ? { opens } : {}),
      ...(tabs.length > 0 ? { tabs } : {}),
    };
  });
}
