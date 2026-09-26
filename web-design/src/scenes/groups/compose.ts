/**
 * Compose group — `#/projects/:p/env/:e/compose/:c/:tab`. The three fixture
 * Composes carry the three lifecycles between them: web (running), worker
 * (partial — worker-cron exited fatal) and metrics (never deployed), and every
 * pane re-reads the same trio.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const EDITOR_HEIGHT = 1100;
const MONITOR_HEIGHT = 1050;
const VOLUMES_HEIGHT = 950;

export const compose: SceneGroupModule = {
  id: 'compose',
  scenes: (paths) => [
    {
      id: 'compose-editor-running',
      title: 'Compose — Editor, running',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      caption:
        'web is fully running and the YAML is in sync: check the status tag, the deploy identity (immutable per ADR-0005) and the editor share one page without the YAML feeling like a form.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [
        { kind: 'tab', from: { scene: 'compose-editor-running' }, to: 'compose-monitor', label: 'Monitor' },
        { kind: 'tab', from: { scene: 'compose-editor-running' }, to: 'compose-volumes-live', label: 'Volumes' },
      ],
    },
    {
      id: 'compose-editor-partial',
      title: 'Compose — Editor, partial',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 2),
      caption:
        'worker is 1 of 2: the exited worker-cron sits in the service list with its state, so check the page says which half is down without opening Monitor.',
      menuPath: ['Projects', 'my-saas', 'production', 'worker'],
      size: { width: 1600, height: EDITOR_HEIGHT },
    },
    {
      id: 'compose-editor-new',
      title: 'Compose — Editor, never deployed',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 3),
      caption:
        'metrics has YAML but no deploy: the primary action is Deploy, and nothing implies containers exist — check Monitor and Logs degrade honestly for this state (their own scenes cover it).',
      menuPath: ['Projects', 'my-saas', 'production', 'metrics'],
      size: { width: 1600, height: EDITOR_HEIGHT },
    },
    {
      id: 'compose-editor-dirty',
      title: 'Compose — Editor, unsaved changes',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      after: 'fixture:compose.draft',
      caption:
        'The draft is seeded through the app\'s own setter, so the dirty banner appears with Discard and Save: a deploy does not pick up the edit until Save, and the banner is where the page says so.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
    },
    {
      id: 'compose-editor-404',
      title: 'Compose — not found',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 999),
      caption:
        'A stale deep link (a deleted Compose, an old tab): the 404 offers the Environment it used to live in, because that is where the replacement would be.',
      menuPath: ['Projects', 'my-saas', 'production', 'deleted'],
      size: { width: 1600, height: 620 },
    },
    {
      id: 'compose-more-menu',
      title: 'Compose — More menu',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      ui: { 'compose.moreOpen': 1 },
      caption:
        'Restart, Stop and Delete live behind one menu so the header keeps a single primary action: check Deploy is the only thing that stays out in the open.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-editor-running', at: [0.965, 0.055] }, to: 'compose-more-menu', label: 'More' }],
    },
    {
      id: 'compose-delete-modal',
      title: 'Compose — Delete Compose',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      ui: { 'compose.deleteOpen': 1, 'danger.typed': 'web' },
      caption:
        'Typed-name confirm at the moment OK enables: the modal names what stops and what is removed, and the frame is here because that instant — enabled but not yet clicked — is the decision.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-editor-running', at: [0.955, 0.09] }, to: 'compose-delete-modal', label: 'More — Delete Compose' }],
    },
    {
      id: 'compose-monitor',
      title: 'Compose — Monitor',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1, 'monitor'),
      caption:
        'Per-service charts with the deploy marker on the axis: check picking a service in the Select re-charts without losing the window, and that the marker sits on a burst worth correlating.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: MONITOR_HEIGHT },
    },
    {
      id: 'compose-monitor-empty',
      title: 'Compose — Monitor, nothing to monitor',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 3, 'monitor'),
      caption:
        'metrics was never deployed, so there are no containers and no samples: the empty state says why (nothing deployed) instead of drawing an empty chart.',
      menuPath: ['Projects', 'my-saas', 'production', 'metrics'],
      size: { width: 1600, height: MONITOR_HEIGHT },
    },
    {
      id: 'compose-monitor-loading',
      title: 'Compose — Monitor, loading',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1, 'monitor'),
      after: 'monitor:loading',
      caption:
        'Loading is a data state held by an adapter: check the skeletons land per chart with the Select and window already in place, so the toolbar never flashes.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: MONITOR_HEIGHT },
    },
    {
      id: 'compose-monitor-partial',
      title: 'Compose — Monitor, exited service',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 2, 'monitor'),
      caption:
        'worker-cron exited, so its line stops mid-window and keeps the history it had: check the chart does not flatline to zero, which would read as "quiet" instead of "dead".',
      menuPath: ['Projects', 'my-saas', 'production', 'worker'],
      size: { width: 1600, height: MONITOR_HEIGHT },
    },
    {
      id: 'compose-volumes-live',
      title: 'Compose — Volumes, resolvable',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1, 'volumes'),
      caption:
        'web resolves to all six Volume shapes — named, bind, read-only, unverifiable and the absent ./retired whose Open is disabled: check every row states where it is on the host and who mounts it.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: VOLUMES_HEIGHT },
    },
    {
      id: 'compose-volumes-unresolvable',
      title: 'Compose — Volumes, not resolvable',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 3, 'volumes'),
      caption:
        'Resolution runs docker compose config, which fails until a first deploy writes the env file: the card quotes the CLI error and offers Deploy as the way out, so the dead end explains its own key.',
      menuPath: ['Projects', 'my-saas', 'production', 'metrics'],
      size: { width: 1600, height: VOLUMES_HEIGHT },
    },
    {
      id: 'compose-volumes-none',
      title: 'Compose — Volumes, nothing mounted',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 4, 'volumes'),
      caption:
        'staging web declares no volumes: the empty state says "nothing mounted" as a fact about the Compose file, not as a resolution failure — the two empties must not look alike.',
      menuPath: ['Projects', 'my-saas', 'staging', 'web'],
      size: { width: 1600, height: VOLUMES_HEIGHT },
    },
    {
      id: 'compose-volumes-loading',
      title: 'Compose — Volumes, resolving',
      group: 'compose',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1, 'volumes'),
      after: 'volumes:loading',
      caption:
        'Resolution is a data fetch held by an adapter: check the skeleton occupies the table\'s rows so the tab does not reflow when the list lands.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: VOLUMES_HEIGHT },
    },
    {
      id: 'compose-logs',
      title: 'Compose — Logs drawer',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      ui: { 'compose.logsOpen': 1 },
      caption:
        'The drawer beside the editor, service filter and line count above a stream: check the newest line is what auto-scroll is showing, and that Copy takes the visible lines only.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-editor-running', at: [0.9, 0.055] }, to: 'compose-logs', label: 'Logs' }],
    },
    {
      id: 'compose-logs-fatal',
      title: 'Compose — Logs, fatal exit',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 2),
      ui: { 'compose.logsOpen': 1 },
      caption:
        'worker-cron\'s last lines are the whole story — fatal ENOENT, exit code 1 — so check the error is findable in a stream that also contains the healthy worker\'s noise.',
      menuPath: ['Projects', 'my-saas', 'production', 'worker'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-editor-partial', at: [0.9, 0.055] }, to: 'compose-logs-fatal', label: 'Logs' }],
    },
    {
      id: 'compose-logs-loading',
      title: 'Compose — Logs, loading',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      ui: { 'compose.logsOpen': 1, 'compose.logsLoading': 1 },
      caption:
        'The drawer opens before the fetch lands: check the skeleton keeps the drawer\'s own shape (filters included) so opening never looks like an empty log.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-logs', at: [0.5, 0.5] }, to: 'compose-logs-loading', label: 'Open before the fetch lands' }],
    },
    {
      id: 'compose-logs-empty',
      title: 'Compose — Logs, empty',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 3),
      ui: { 'compose.logsOpen': 1 },
      caption:
        'metrics has never run, so there is no log: the drawer must say "no output" as a state, because an empty pre block reads as a broken stream.',
      menuPath: ['Projects', 'my-saas', 'production', 'metrics'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-editor-new', at: [0.9, 0.055] }, to: 'compose-logs-empty', label: 'Logs' }],
    },
    {
      id: 'compose-fullscreen',
      title: 'Compose — Fullscreen editor',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      ui: { 'compose.fullscreenOpen': 1 },
      caption:
        'The editor at full viewport with the variables rail beside it: check a ${VAR} reference in the YAML is copyable from the rail, which is the reason the rail exists.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-editor-running', at: [0.865, 0.055] }, to: 'compose-fullscreen', label: 'Edit fullscreen' }],
    },
    {
      id: 'compose-fullscreen-empty-rail',
      title: 'Compose — Fullscreen editor, rail filtered to nothing',
      group: 'compose',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.compose(1, 1, 1),
      ui: { 'compose.fullscreenOpen': 1, 'fullscreen.railQuery': 'GRAFANA' },
      caption:
        'A rail query nothing matches narrows to an empty list while the edit continues underneath: the YAML must stay interactive, because the rail is a reference and not a gate.',
      menuPath: ['Projects', 'my-saas', 'production', 'web'],
      size: { width: 1600, height: EDITOR_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'compose-fullscreen', at: [0.87, 0.35] }, to: 'compose-fullscreen-empty-rail', label: 'Filter the rail' }],
    },
  ],
};
