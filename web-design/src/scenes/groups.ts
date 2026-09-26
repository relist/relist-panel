/**
 * The board's columns, in menu order — the single source of the group order.
 *
 * `registry.ts` assembles `SCENES` by mapping over this list, and
 * `scripts/registry.test.mjs` rebuilds the registry from it the same way, so
 * the two can never disagree about what comes first. The order is the sider
 * menu's own flow: top-level screens, then the drill-down screens a Recent
 * Projects item opens, then the design-system pages and the review surfaces.
 *
 * Data-only: a type-only import is erased at runtime, so `node --test` loads
 * this file unchanged.
 */

import type { SceneGroupId } from './types';

export type SceneGroup = { id: SceneGroupId; label: string };

export const GROUPS: readonly SceneGroup[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'projects', label: 'Projects' },
  { id: 'monitoring', label: 'Monitoring' },
  { id: 'project', label: 'Project' },
  { id: 'compose', label: 'Compose' },
  { id: 'volume-explorer', label: 'Volume Explorer' },
  { id: 'design-system', label: 'Design system' },
  { id: 'auth', label: 'Auth' },
  { id: 'feedback', label: 'Feedback' },
];
