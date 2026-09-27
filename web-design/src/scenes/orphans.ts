/**
 * Reachability, read straight off the registry (ticket 13) — the one question
 * a whole-registry view answers that the board and the task flows cannot.
 *
 * What a declared link means here is narrow on purpose: links are `opens`
 * triggers and `tab` panes, *not* routes and not menu items. So "no link in" is
 * not the same as "unreachable", and the report says so in three separate
 * lists rather than one scaremongering one:
 *
 *  - **`unlinkedOverlays`** — an overlay nothing triggers. This is the
 *    actionable list: a dialog with no trigger is a declared state with no way
 *    to open it, and it is empty today (asserted by `scripts/registry.test.mjs`).
 *  - **`noIncomingLink`** — everything else nothing links to, which by design
 *    is large: entry screens are reached by their route or a menu item, and a
 *    screen's forced states (loading, empty, error) are states of a screen, not
 *    destinations.
 *  - **`groupsWithoutMenu`** — groups no menu item names (Compose and Volume
 *    Explorer): reached by drilling into a screen, per the sider.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { Scene, SceneGroupId, SceneKind } from './types';

export type OrphanRow = {
  sceneId: string;
  kind: SceneKind;
  group: SceneGroupId;
  /** The incoming links, as `kind:from` — empty for a row in `noIncomingLink`. */
  incoming: string[];
};

export type OrphanReport = {
  noIncomingLink: OrphanRow[];
  unlinkedOverlays: OrphanRow[];
  groupsWithoutMenu: SceneGroupId[];
};

/** `menuGroups` is the set of group ids some menu item targets. Pure. */
export function orphanReport(scenes: readonly Scene[], menuGroups: ReadonlySet<string>): OrphanReport {
  const incoming = new Map<string, string[]>(scenes.map((scene) => [scene.id, []]));
  for (const scene of scenes) {
    for (const link of scene.links ?? []) {
      incoming.get(link.to)?.push(`${link.kind}:${link.from.scene}`);
    }
  }

  const noIncomingLink = scenes
    .filter((scene) => (incoming.get(scene.id) ?? []).length === 0)
    .map((scene) => ({
      sceneId: scene.id,
      kind: scene.kind,
      group: scene.group,
      incoming: incoming.get(scene.id) ?? [],
    }));

  return {
    noIncomingLink,
    unlinkedOverlays: noIncomingLink.filter((row) => row.kind === 'overlay'),
    groupsWithoutMenu: [...new Set(scenes.map((scene) => scene.group))].filter((group) => !menuGroups.has(group)),
  };
}
