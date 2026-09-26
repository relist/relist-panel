/**
 * The scene registry — every frame the board may draw, in menu order, with the
 * navigation the flow layer draws (ticket 04). This is the plan's second half's
 * source of truth: nothing is rendered or laid out that is not declared here.
 *
 * Shape of the assembly:
 *
 *  - `GROUPS` (`groups.ts`) owns the **order** — the menu's order, which is the
 *    board's column order.
 *  - each `groups/<id>.ts` owns its **scenes**, exported as a function of
 *    `paths` so every `hash` is built through `app/router.ts` at assembly time
 *    (a route rename moves the registry with it) while the module itself keeps
 *    zero runtime imports — which is what lets `scripts/registry.test.mjs`
 *    rebuild this exact array under plain Node and assert the invariants.
 *  - this file only **wires**: map the ids to their modules, flat-map in group
 *    order, index by id. Adding a scene is a registry edit in one group file;
 *    adding a group touches `groups.ts` and the map below and nowhere else.
 */

import { paths } from '../app/router';

import { GROUPS } from './groups';
import { auth } from './groups/auth';
import { compose } from './groups/compose';
import { designSystem } from './groups/design-system';
import { feedback } from './groups/feedback';
import { monitoring } from './groups/monitoring';
import { overview } from './groups/overview';
import { project } from './groups/project';
import { projects } from './groups/projects';
import { volumeExplorer } from './groups/volume-explorer';
import type { Scene, SceneGroupModule, SceneGroupId, ScenePaths } from './types';

/** Every group id has exactly one module — the type on this map enforces it. */
const GROUP_MODULES: Record<SceneGroupId, (paths: ScenePaths) => Scene[]> = {
  auth: auth.scenes,
  compose: compose.scenes,
  'design-system': designSystem.scenes,
  feedback: feedback.scenes,
  monitoring: monitoring.scenes,
  overview: overview.scenes,
  project: project.scenes,
  projects: projects.scenes,
  'volume-explorer': volumeExplorer.scenes,
};

/** The assembled catalogue: group order is the menu's order; a group keeps its module's order. */
export const SCENES: readonly Scene[] = GROUPS.flatMap((group) => {
  const scenes: SceneGroupModule['scenes'] = GROUP_MODULES[group.id];
  return scenes(paths);
});

/** Look a scene up by id — feedback quotes it, and it is the frame's name on the board. */
export function sceneById(id: string): Scene | undefined {
  return SCENES.find((scene) => scene.id === id);
}
