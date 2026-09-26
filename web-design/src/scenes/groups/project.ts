/**
 * Project group — `#/projects/:p/env/:e/:tab`, the drill-down screen and its
 * overlays. The fixtures give my-saas three Environments: production (full),
 * staging (one Compose) and preview (nothing), which is where the empty states
 * are real data rather than forced booleans.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const COMPOSES_HEIGHT = 950;
const VARIABLES_HEIGHT = 880;
const SETTINGS_HEIGHT = 1020;

export const project: SceneGroupModule = {
  id: 'project',
  scenes: (paths) => [
    {
      id: 'project-composes',
      title: 'Project — Composes',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.project(1),
      caption:
        'my-saas / production: the Environment switcher keeps the context while the table lists web, worker and metrics — check the mixed states (running, partial, never deployed) read at a glance in one row.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
      links: [
        { kind: 'tab', from: { scene: 'project-composes' }, to: 'project-variables', label: 'Variables' },
        { kind: 'tab', from: { scene: 'project-composes' }, to: 'project-settings', label: 'Settings' },
      ],
    },
    {
      id: 'project-composes-empty',
      title: 'Project — Composes, empty Environment',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.environment(1, 3, 'composes'),
      caption:
        'preview has no Composes, so the empty state offers "Write the first Compose" — the copy names the Environment it is about, because an empty tab must not read as a load failure.',
      menuPath: ['Projects', 'my-saas', 'preview'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
    },
    {
      id: 'project-composes-row-menu',
      title: 'Project — Composes row menu',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.project(1),
      ui: { 'project.rowMenu.web': 1 },
      caption:
        'Deploy, Restart, Stop and Delete per Compose row, addressed by naming the row: check that the destructive entry is separated from the lifecycle ones.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-composes', at: [0.985, 0.3] }, to: 'project-composes-row-menu', label: 'Row menu' }],
    },
    {
      id: 'project-404',
      title: 'Project — not found',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.project(99),
      caption:
        'A stale link to a deleted Project must dead-end politely: the 404 names what is missing and offers the way back to Projects instead of an empty shell.',
      menuPath: ['Projects', 'deleted'],
      size: { width: 1600, height: 620 },
    },
    {
      id: 'project-zero-envs',
      title: 'Project — no Environments',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.project(4),
      after: 'seed:project.zeroEnvs',
      caption:
        'A Project can exist before its first Environment: the switcher is replaced by a single call to create one — no fixture Project has zero Environments, so the state is adapter-backed (see ticket Comments).',
      menuPath: ['Projects', 'blog'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
    },
    {
      id: 'project-new-compose-empty',
      title: 'Project — New Compose, empty YAML',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.project(1),
      ui: { 'project.composeModalOpen': 1 },
      caption:
        'OK is disabled until there is YAML: a Compose is created from a real file, never from a name alone — check the disabled state explains what is missing.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-composes', at: [0.95, 0.08] }, to: 'project-new-compose-empty', label: 'New Compose' }],
    },
    {
      id: 'project-new-compose-filled',
      title: 'Project — New Compose, YAML pasted',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.project(1),
      ui: { 'project.composeModalOpen': 1 },
      after: 'fixture:project.composeYAML',
      caption:
        'The starter YAML arrives through the app\'s own setter, so OK enables: check the editor is where the deploy actually begins, not a name field with a file upload bolted on.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-new-compose-empty', at: [0.5, 0.6] }, to: 'project-new-compose-filled', label: 'Paste YAML' }],
    },
    {
      id: 'project-new-env-modal',
      title: 'Project — New Environment',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.project(1),
      ui: { 'project.environmentModalOpen': 1 },
      caption:
        'One field, because an Environment is a scope and not a deployment: check the modal does not imply it will deploy anything.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-composes', at: [0.62, 0.09] }, to: 'project-new-env-modal', label: 'New Environment' }],
    },
    {
      id: 'project-delete-compose-modal',
      title: 'Project — Delete Compose',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.project(1),
      after: 'object:project.deletingCompose',
      caption:
        'The typed-name confirm seeded with web through the app\'s own setter: the consequence line says what happens to the containers, so check the copy and the disabled OK agree that this is deliberate.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: COMPOSES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-composes', at: [0.985, 0.38] }, to: 'project-delete-compose-modal', label: 'Row menu — Delete' }],
    },
    {
      id: 'project-delete-env-popconfirm',
      title: 'Project — Delete Environment',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'settings'),
      ui: { 'project.deleteEnv.staging': 1 },
      caption:
        'The Popconfirm carries the arithmetic ("this stops and removes every Compose in it"), addressed per row: a lightweight confirm is honest only while the consequence fits in one sentence.',
      menuPath: ['Projects', 'my-saas', 'staging'],
      size: { width: 1600, height: SETTINGS_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-settings', at: [0.985, 0.4] }, to: 'project-delete-env-popconfirm', label: 'Delete Environment' }],
    },
    {
      id: 'project-delete-modal',
      title: 'Project — Delete Project',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'settings'),
      ui: { 'project.deleteProjectOpen': 1, 'danger.typed': 'my-saas' },
      caption:
        'Danger zone, name typed: OK has just enabled because the input matches — the frame exists to show that moment, where the cost of the action is retyping what you are about to lose.',
      menuPath: ['Projects', 'my-saas'],
      size: { width: 1600, height: SETTINGS_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-settings', at: [0.5, 0.86] }, to: 'project-delete-modal', label: 'Delete Project' }],
    },
    {
      id: 'project-variables',
      title: 'Project — Variables',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      caption:
        'production\'s six keys, secrets masked by default: check the reveal control is per value and the type tag (secret/plain) is visible without exposing anything.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
      links: [
        { kind: 'tab', from: { scene: 'project-variables' }, to: 'project-composes', label: 'Composes' },
        { kind: 'tab', from: { scene: 'project-variables' }, to: 'project-settings', label: 'Settings' },
      ],
    },
    {
      id: 'project-variables-revealed',
      title: 'Project — Variables, one revealed',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      ui: { 'variables.revealed.DATABASE_URL': 1 },
      caption:
        'Reveal is addressed per key, so exactly one value is exposed while its neighbours stay masked — check that the revealed state is visibly deliberate (icon state flipped), not a rendering bug.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
    },
    {
      id: 'project-variables-filtered-empty',
      title: 'Project — Variables, filter matches nothing',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      ui: { 'variables.query': 'GRAFANA' },
      caption:
        'The search narrows to nothing: the empty row names the query back, so a zero reads as "no match for GRAFANA" and not as lost variables.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
    },
    {
      id: 'project-variables-empty',
      title: 'Project — Variables, empty Environment',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.environment(1, 3, 'variables'),
      caption:
        'preview has no keys yet: the empty state points at Paste a .env, because the honest way to fill this table is the file the operator already has.',
      menuPath: ['Projects', 'my-saas', 'preview'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
    },
    {
      id: 'project-variable-new-modal',
      title: 'Project — New variable',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      ui: { 'variables.modalOpen': 1 },
      caption:
        'Key, value and the secret switch on one modal: check the key rules (letters, digits, underscores) are stated where they will be violated.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-variables', at: [0.94, 0.07] }, to: 'project-variable-new-modal', label: 'New variable' }],
    },
    {
      id: 'project-variable-edit-modal',
      title: 'Project — Edit variable',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      after: 'object:variables.editing',
      caption:
        'The same modal prefilled with DATABASE_URL through the app\'s own setter: check the key is editable and the value arrives masked exactly as the row showed it.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-variables', at: [0.97, 0.28] }, to: 'project-variable-edit-modal', label: 'Row — Edit' }],
    },
    {
      id: 'project-paste-env',
      title: 'Project — Paste a .env, parsed',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      ui: { 'variables.bulkOpen': 1 },
      after: 'fixture:variables.bulkText',
      caption:
        'The paste is the state: the fixture .env is parsed into a preview table before anything is committed, so check the "Replace N variables with M" statement is the OK button\'s own label.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-variables', at: [0.9, 0.07] }, to: 'project-paste-env', label: 'Paste a .env' }],
    },
    {
      id: 'project-paste-env-duplicates',
      title: 'Project — Paste a .env, duplicate key',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      ui: { 'variables.bulkOpen': 1 },
      after: 'fixture:variables.bulkDuplicate',
      caption:
        'A duplicate key refuses the whole paste, naming the offending key and disabling OK: a bulk replace that silently picked a winner would be the worst kind of surprise.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-paste-env', at: [0.5, 0.55] }, to: 'project-paste-env-duplicates', label: 'Paste with a duplicate key' }],
    },
    {
      id: 'project-delete-variable-popconfirm',
      title: 'Project — Delete variable',
      group: 'project',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'variables'),
      ui: { 'variables.deletePopconfirm.DATABASE_URL': 1 },
      caption:
        'Deleting one key costs one Popconfirm naming the key, not a typed-name modal: check the escalation is proportional — this is reversible by pasting the value back.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: VARIABLES_HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'project-variables', at: [0.985, 0.32] }, to: 'project-delete-variable-popconfirm', label: 'Row — Delete' }],
    },
    {
      id: 'project-settings',
      title: 'Project — Settings',
      group: 'project',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.environment(1, 1, 'settings'),
      caption:
        'Project details, the Environments table and the danger zone on one tab: check that Delete Project is visually the last thing on the page and the only red one.',
      menuPath: ['Projects', 'my-saas', 'production'],
      size: { width: 1600, height: SETTINGS_HEIGHT },
      links: [
        { kind: 'tab', from: { scene: 'project-settings' }, to: 'project-composes', label: 'Composes' },
        { kind: 'tab', from: { scene: 'project-settings' }, to: 'project-variables', label: 'Variables' },
      ],
    },
  ],
};
