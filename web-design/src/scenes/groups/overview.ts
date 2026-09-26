/**
 * Overview group — `#/`, plus the shell overlays hosted on it.
 *
 * The shell has no route of its own (it wraps every screen), so its overlays
 * are forced states on the overview route: the account menu and the command
 * palette are opened from the header, and the overview is the screen a reviewer
 * pictures behind them.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const HEIGHT = 1000;

export const overview: SceneGroupModule = {
  id: 'overview',
  scenes: (paths) => [
    {
      id: 'overview',
      title: 'Overview',
      group: 'overview',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.overview,
      caption:
        'The landing read: the attention alert names the Composes that are not fully up (worker is partial, registry is stopped), and beneath it the live-handle banner, activity and every Compose — check that the page answers "is anything wrong?" before you scroll.',
      menuPath: ['Overview'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'overview-all-clear',
      title: 'Overview — all clear',
      group: 'overview',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.overview,
      after: 'seed:overview.allClear',
      caption:
        'Attention is derived, not stored: with every Compose fully running the alert disappears and the Needs-attention card shows the empty state, so check that "All clear" reads as information and not as a broken page.',
      menuPath: ['Overview'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'overview-refreshing',
      title: 'Overview — refreshing',
      group: 'overview',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.overview,
      ui: { 'overview.refreshing': 1 },
      caption:
        'Refresh statuses re-checks every Compose on the host; the spinner holds the button without blanking the page, which is the difference between "busy" and "lost".',
      menuPath: ['Overview'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'overview-handle-orphaned',
      title: 'Overview — orphaned Volume Handle',
      group: 'overview',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.overview,
      ui: { 'store.handles': '9:/srv/retired-data' },
      caption:
        'A Handle whose Compose is gone can still be open, so the banner names it "a deleted Compose" instead of a dead link — check that the offer is Close, not Browse.',
      menuPath: ['Overview'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'shell-account-menu',
      title: 'Shell — account menu',
      group: 'overview',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.overview,
      ui: { 'shell.accountMenu': 1 },
      caption:
        'The header is on every screen, so its one dropdown is reviewed once: check that sign-out is separated from navigation, because it is the only control here that ends the session.',
      menuPath: ['Overview'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'overview', at: [0.982, 0.03] }, to: 'shell-account-menu', label: 'Account' }],
    },
    {
      id: 'shell-command-palette',
      title: 'Shell — command palette',
      group: 'overview',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.overview,
      ui: { 'shell.paletteOpen': 1 },
      caption:
        'Keyboard-first navigation over the mock\'s real routes: check that the hits are Composes and screens you would actually jump to, not a second menu.',
      menuPath: ['Overview'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'overview', at: [0.945, 0.03] }, to: 'shell-command-palette', label: 'Search' }],
    },
    {
      id: 'shell-command-palette-empty',
      title: 'Shell — command palette, no match',
      group: 'overview',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.overview,
      ui: { 'shell.paletteOpen': 1, 'palette.query': 'deploy' },
      caption:
        'Typing a query nothing matches is the palette\'s only dead end; the empty copy names the query back so it reads as "no match for deploy", not "broken".',
      menuPath: ['Overview'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'shell-command-palette', at: [0.5, 0.1] }, to: 'shell-command-palette-empty', label: 'Type a query' }],
    },
  ],
};
