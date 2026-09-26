/**
 * Projects group — `#/projects`, the landing grid and its overlays.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const HEIGHT = 860;

export const projects: SceneGroupModule = {
  id: 'projects',
  scenes: (paths) => [
    {
      id: 'projects-cards',
      title: 'Projects — cards',
      group: 'projects',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.projects,
      caption:
        'The default view: one card per Project with its Compose rollup, so a degraded app is visible before you open anything — check the partial worker shows on my-saas without drilling in.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'projects-table',
      title: 'Projects — table',
      group: 'projects',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.projects,
      ui: { 'projects.view': 'table' },
      caption:
        'Same data, operator density: sortable columns and per-row actions for someone running many Projects — check that the rollup survives the denser layout.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'projects-filtered-empty',
      title: 'Projects — filter matches nothing',
      group: 'projects',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.projects,
      ui: { 'projects.query': 'billing' },
      caption:
        'A filter that matches nothing must say so against the query it was given — the empty card quotes "billing" back, so a typo reads as a typo and not as lost Projects.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'projects-empty',
      title: 'Projects — nothing yet',
      group: 'projects',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.projects,
      after: 'projects:empty',
      caption:
        'First-run has no Projects to roll up: the empty state sells the next action (Create a Project) instead of an empty grid — the one Projects state the store cannot seed today, reached through an adapter (see ticket Comments).',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'projects-row-menu',
      title: 'Projects — row menu',
      group: 'projects',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.projects,
      ui: { 'projects.view': 'table', 'projects.rowMenu.landing': 1 },
      caption:
        'Per-row state is addressed by naming the row, or one boolean would open every menu at once — check that Edit and Delete sit behind one click and not inline.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'projects-table', at: [0.985, 0.28] }, to: 'projects-row-menu', label: 'Row menu' }],
    },
    {
      id: 'projects-new-modal',
      title: 'Projects — New Project',
      group: 'projects',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.projects,
      ui: { 'projects.modalOpen': 1 },
      caption:
        'Creation is two fields on purpose: check that the modal promises no Environment or Compose decisions, because those belong to the Project screen after it exists.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'projects-cards', at: [0.955, 0.05] }, to: 'projects-new-modal', label: 'New Project' }],
    },
    {
      id: 'projects-new-modal-errors',
      title: 'Projects — New Project, invalid',
      group: 'projects',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.projects,
      ui: { 'projects.modalOpen': 1 },
      after: 'submit:projects.new.empty',
      caption:
        'Validation only exists after a submit, so the frame is the real handler\'s result: errors under the name field, the modal still open, nothing the user typed lost.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'projects-new-modal', at: [0.62, 0.88] }, to: 'projects-new-modal-errors', label: 'Submit empty' }],
    },
    {
      id: 'projects-edit-modal',
      title: 'Projects — Edit Project',
      group: 'projects',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.projects,
      after: 'object:projects.editing',
      caption:
        'The same modal as New, prefilled through the app\'s own setter with a real Project: check the title says Edit and that name and description arrive as stored.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'projects-cards', at: [0.93, 0.32] }, to: 'projects-edit-modal', label: 'Card menu — Edit' }],
    },
    {
      id: 'projects-delete-modal',
      title: 'Projects — Delete Project',
      group: 'projects',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.projects,
      after: 'object:projects.deleting',
      caption:
        'The typed-name confirm for landing: OK stays disabled until the name matches, because deleting a Project takes its Environments and Composes with it and retyping the name is the cost of meaning it.',
      menuPath: ['Projects'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'projects-table', at: [0.985, 0.36] }, to: 'projects-delete-modal', label: 'Row menu — Delete' }],
    },
  ],
};
