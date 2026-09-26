/**
 * Volume Explorer group — `#/…/compose/:c/volumes/:volumeId[/path]`, the
 * file-level screen and the Start Handle dialog that gates it.
 *
 * The uploads tree on Compose 1 carries every shape the screen can meet:
 * nested directories, an empty directory, a 4.2 MB file past the editor cap, a
 * symlink that stays inside, one that escapes, an unreadable directory, and —
 * since ticket 04's fixture — a Volume whose source is absent.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const HEIGHT = 1000;

export const volumeExplorer: SceneGroupModule = {
  id: 'volume-explorer',
  scenes: (paths) => [
    {
      id: 'volume-explorer-root',
      title: 'Volume Explorer — root listing',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      caption:
        'The uploads root in one frame: the running-stack write warning (rw handle, api up), the escaping legacy symlink, the 4.2 MB export.csv and the filename with a newline — check the screen is honest about all four without a click.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-subdir',
      title: 'Volume Explorer — subdirectory',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads', 'invoices/2026-09'),
      caption:
        'Three levels down via the breadcrumb: check the crumbs stay navigable (every segment clickable) and the listing is still one Volume, not a new context.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes', 'invoices/2026-09'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-no-handle',
      title: 'Volume Explorer — no handle open',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'store.handles': 'none' },
      caption:
        'A Handle exists only while someone has it open, so with none the screen shows an explanation and one button — check the copy explains the model instead of implying an error.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-expired',
      title: 'Volume Explorer — handle closed by idle',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'store.handles': 'none', 'volume.expiredHere': 1 },
      caption:
        'The handle stopped itself after 30 idle minutes: the alert says nothing was changed and reopening costs one click, because an auto-stop must never read as data loss.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-unreadable',
      title: 'Volume Explorer — unreadable directory',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads', 'avatars'),
      caption:
        'avatars is 0700 root:root and the handle runs as uid 999: the error quotes the helper message with the uid in it, because the uid is the thing an operator can actually change.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes', 'avatars'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-404',
      title: 'Volume Explorer — Volume not found',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/nope'),
      caption:
        'A Volume removed from the Compose since the link was made: the 404 says so and offers the way back to the Volumes tab, which is where the current list lives.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: 620 },
    },
    {
      id: 'volume-explorer-compose-unresolvable',
      title: 'Volume Explorer — Compose not resolvable',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 3, '3:/var/lib/grafana'),
      caption:
        'metrics cannot resolve its volumes at all, so the explorer cannot open a handle: check the screen refuses upstream of the dialog instead of pretending the Volume exists.',
      menuPath: ['Projects', 'my-saas', 'production', 'metrics', 'Volumes'],
      size: { width: 1600, height: 620 },
    },
    {
      id: 'volume-explorer-loading',
      title: 'Volume Explorer — loading',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      after: 'volume:loading',
      caption:
        'Listing a directory is a fetch, held by an adapter: check the skeleton keeps the toolbar and breadcrumb so the screen does not jump when the rows arrive.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-readonly',
      title: 'Volume Explorer — read-only handle',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/config'),
      after: 'seed:volume.handleRo',
      caption:
        'config is a read-only bind opened ro: the banner says editing, uploading and deleting are off, and the row actions are gone rather than disabled-invisible — the seeded handle is rw, so this frame is adapter-backed (ticket Comments).',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes', 'config'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-editor-open',
      title: 'Volume Explorer — file editor open',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      after: 'object:volume.editing',
      caption:
        'README.txt open in the editor card below the listing, seeded through the app\'s own setter: check Save writes back to the Volume and Cancel leaves without a toast.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-too-large',
      title: 'Volume Explorer — too large to edit',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      after: 'object:volume.editorTooLarge',
      caption:
        'export.csv is 4.2 MB past the 1 MB cap: the refusal names the size, the cap and the alternative (download), because the honest move is to refuse Monaco rather than hang it.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-upload-size',
      title: 'Volume Explorer — upload refused, too large',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      after: 'seed:volume.uploadTooLarge',
      caption:
        'An oversized upload is refused with the file\'s name and the limit in the alert, reached through the app\'s own refusal path so the copy is the app\'s: check nothing half-uploaded remains.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-upload-space',
      title: 'Volume Explorer — upload refused, no space',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      after: 'seed:volume.uploadNoSpace',
      caption:
        'The other refusal: the Volume is out of space, so the alert names the volume rather than the file — the two upload failures must be tellable apart.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-empty-dir',
      title: 'Volume Explorer — empty directory',
      group: 'volume-explorer',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads', 'incoming'),
      caption:
        'incoming is emptied by the cleanup job: an empty table is a true fact about the Volume here, so check it reads as "no files" and not as a failed listing.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes', 'incoming'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'volume-explorer-new-file',
      title: 'Volume Explorer — New file',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'volume.newEntry': 'file' },
      caption:
        'The modal states where the file lands and who will own it (the handle\'s uid) before it exists: check OK stays disabled while the name is empty.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.83, 0.05] }, to: 'volume-explorer-new-file', label: 'New file' }],
    },
    {
      id: 'volume-explorer-new-folder',
      title: 'Volume Explorer — New folder',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'volume.newEntry': 'dir' },
      caption:
        'Same modal, different title and placeholder: one component serving both, so check nothing about the copy still says "file" in the folder state.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.865, 0.05] }, to: 'volume-explorer-new-folder', label: 'New folder' }],
    },
    {
      id: 'volume-explorer-download-dialog',
      title: 'Volume Explorer — Download as archive',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'volume.downloadOpen': 1 },
      caption:
        'The disclosure is the point: this tree contains an escaping symlink, and the dialog counts it before you extract — check the warning is above the OK button, not below the fold.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.795, 0.05] }, to: 'volume-explorer-download-dialog', label: 'Download' }],
    },
    {
      id: 'volume-explorer-row-menu',
      title: 'Volume Explorer — row menu',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'volume.rowMenu.latest': 1 },
      caption:
        'Download, Rename and Delete per row, addressed by naming the row: check Rename says out loud that it is out of scope for the design pass instead of failing silently.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.99, 0.3] }, to: 'volume-explorer-row-menu', label: 'Row menu' }],
    },
    {
      id: 'volume-explorer-delete-popconfirm',
      title: 'Volume Explorer — delete entry',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'volume.deletePopconfirm.legacy': 1 },
      caption:
        '"Not a trash — there is no undo", naming the entry and, for a directory, everything under it: the audit also found this Popconfirm has no onConfirm yet (ticket 02 §5.6), so the confirm is currently a no-op — recorded, not fixed here.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.965, 0.42] }, to: 'volume-explorer-delete-popconfirm', label: 'Row — Delete' }],
    },
    {
      id: 'start-handle-rw',
      title: 'Start Handle — read-write, stack running',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'volume.startOpen': 1 },
      caption:
        'The default (rw, uid 999 from the container) with api and redis up, so the write warning names them: this is the banner an operator most needs to read before typing, because there is no confirmation gate after it.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.93, 0.045] }, to: 'start-handle-rw', label: 'Open Volume' }],
    },
    {
      id: 'start-handle-ro',
      title: 'Start Handle — read-only',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/config'),
      ui: { 'volume.startOpen': 1, 'startHandle.mode': 'ro' },
      caption:
        'Switching to ro on the read-only bind: the dialog says what ro costs (no edits) and nothing about corruption, because a read-only mount cannot corrupt a running stack.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes', 'config'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.93, 0.045] }, to: 'start-handle-ro', label: 'Open Volume — read-only' }],
    },
    {
      id: 'start-handle-unverifiable',
      title: 'Start Handle — unverifiable path',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/srv/legacy-images'),
      ui: { 'volume.startOpen': 1 },
      caption:
        'An absolute bind outside the data directory cannot be stat-ed without mounting it: the warning says so and names the root:root 0755 an accidental mount would create — honesty instead of a guess.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes', 'legacy'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.93, 0.045] }, to: 'start-handle-unverifiable', label: 'Open Volume' }],
    },
    {
      id: 'start-handle-absent',
      title: 'Start Handle — absent path refused',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/retired'),
      ui: { 'volume.startOpen': 1 },
      caption:
        './retired is declared but was never provisioned, so OK is disabled: mounting it would create it, and the refusal says what would — this is the state ticket 04\'s fixture addition made reachable.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes', 'retired'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.93, 0.045] }, to: 'start-handle-absent', label: 'Open Volume' }],
    },
    {
      id: 'start-handle-root',
      title: 'Start Handle — uid typed by hand',
      group: 'volume-explorer',
      kind: 'overlay',
      viewports: ['desktop'],
      hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
      ui: { 'volume.startOpen': 1, 'startHandle.uid': '0', 'startHandle.gid': '0' },
      caption:
        'Typing 0:0 flips the source tag to "set by hand" and raises the root warning: the frame exists because a manual uid is sometimes the only fix and always the dangerous one.',
      menuPath: ['Projects', 'my-saas', 'production', 'web', 'Volumes'],
      size: { width: 1600, height: HEIGHT },
      links: [{ kind: 'opens', from: { scene: 'volume-explorer-root', at: [0.93, 0.045] }, to: 'start-handle-root', label: 'Open Volume — set uid by hand' }],
    },
  ],
};
