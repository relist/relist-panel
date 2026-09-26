/**
 * Design system group — the guidelines page. One frame, deliberately tall:
 * the page is the design system's own documentation, and a frame cut at the
 * fold would review the crop and not the rules.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

export const designSystem: SceneGroupModule = {
  id: 'design-system',
  scenes: (paths) => [
    {
      id: 'guidelines',
      title: 'Guidelines',
      group: 'design-system',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.guidelines,
      caption:
        'The panel documenting itself — spacing, type, tables, feedback rules and the live examples under each: one tall frame on purpose, so a rule and its example are reviewed in the same glance.',
      menuPath: ['Design system', 'Guidelines'],
      size: { width: 1600, height: 2400 },
    },
  ],
};
