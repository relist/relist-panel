/**
 * Monitoring group — `#/monitoring`, the host's six charts across the windows.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const HEIGHT = 1150;

export const monitoring: SceneGroupModule = {
  id: 'monitoring',
  scenes: (paths) => [
    {
      id: 'monitoring',
      title: 'Monitoring',
      group: 'monitoring',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.monitoring,
      caption:
        'One host, six series, at the default 1h window: memory rides its sawtooth into the warn tone, so check the chart\'s colour and the headline agree about how close to the ceiling this is.',
      menuPath: ['Monitoring'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'monitoring-loading',
      title: 'Monitoring — loading',
      group: 'monitoring',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.monitoring,
      after: 'monitoring:loading',
      caption:
        'Six skeletons, not one: loading is a data state, so it is held by an adapter rather than a forced boolean — check the skeleton keeps each chart\'s slot so the page does not jump when data lands.',
      menuPath: ['Monitoring'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'monitoring-6h',
      title: 'Monitoring — 6h window',
      group: 'monitoring',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.monitoring,
      ui: { 'monitoring.window': '6h' },
      caption:
        'Beyond the hour the panel rolls up to one-minute samples: check the 6h window still shows the deploy spike and the backup bump instead of flattening them away.',
      menuPath: ['Monitoring'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'monitoring-24h',
      title: 'Monitoring — 24h window, partially collected',
      group: 'monitoring',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.monitoring,
      ui: { 'monitoring.window': '24h' },
      caption:
        'The panel has been up 187 minutes, so a 24h window has 187 of 1440 minutes: check the "collecting" note says what is missing instead of drawing the line as if the host were idle all day.',
      menuPath: ['Monitoring'],
      size: { width: 1600, height: HEIGHT },
    },
  ],
};
