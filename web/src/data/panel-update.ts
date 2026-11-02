/**
 * Panel update wire types (`/api/panel-update/*`) and the page's pure rules:
 * which state the page is in, how a failed run reads, and the commands an
 * operator runs on the host when the page cannot help any more.
 */

import { ApiError, api } from './api';

export type RunState =
  | 'quiescing'
  | 'backing-up'
  | 'writing-files'
  | 'compose-up'
  | 'waiting-health'
  | 'rolling-back'
  | 'succeeded'
  | 'rolled-back'
  | 'rollback-failed'
  | 'aborted'
  | 'interrupted';

export type ManualStep = { id: string; text: string };

export type PanelUpdateAvailable = {
  version: string;
  releasedAt: string;
  notesUrl: string;
  manualSteps: ManualStep[];
};

export type PanelUpdateRun = {
  id: string;
  state: RunState;
  terminal: boolean;
  error: string;
  rollbackError: string;
  /** What a quiescing run waits for, such as `Deployment api #3`; empty when nothing. */
  waitingOn: string;
  /** A caveat about a run that went on, such as a panel that did not answer. */
  note: string;
  fromVersion: string;
  toVersion: string;
  startedAt: number;
  /** The run directory that holds the backup; empty before the backup is complete. */
  backupPath: string;
  /** Each backed-up Install file and .env: where it belongs, and its copy in `backupPath`. */
  backupFiles: PanelUpdateBackupFile[];
};

export type PanelUpdateBackupFile = { path: string; copy: string };

export type PanelUpdateStatus = {
  version: string;
  development: boolean;
  message?: string;
  available: PanelUpdateAvailable | null;
  checkEnabled: boolean;
  lastCheckAt: string | null;
  lastError: string;
  install: { available: boolean; reason: string };
  locked: boolean;
  run: PanelUpdateRun | null;
};

export type PreflightChange = '' | 'not-edited' | 'edited-merged' | 'edited-conflict';

export type InvariantResult = {
  kind: string;
  text: string;
  ok: boolean;
  detail?: string;
  source?: string;
};

export type PreflightReport = {
  id: string;
  createdAt: string;
  fromVersion: string;
  toVersion: string;
  notesUrl: string;
  ready: boolean;
  failed: { step: string; message: string } | null;
  needsManualUpdate: boolean;
  unknownKinds: string[];
  readmeUrl: string;
  /** True when the newest release is not newer than the running version. */
  upToDate: boolean;
  image: string;
  change: PreflightChange;
  diff: string;
  conflict: { ours: string; theirs: string; merged: string; hunks: number } | null;
  invariants: InvariantResult[];
  recreated: string[];
  traefikRecreated: boolean;
  manualSteps: ManualStep[];
};

/** The live state the update waits on, shown next to the report. */
export type PreflightGate = { installAvailable: boolean; migrationLocked: boolean; deploysBusy: boolean; updateRunning: boolean };

/** `POST /api/panel-update/preflight`: the report of the update Container and the live gate. */
export type PreviewResponse = { report: PreflightReport; gate: PreflightGate };

/** The steps of a run in order, as the Updating state lists them. */
export const UPDATE_STEPS: { state: RunState; title: string }[] = [
  { state: 'quiescing', title: 'Pause deployments and changes' },
  { state: 'backing-up', title: 'Back up the database and the Install files' },
  { state: 'writing-files', title: 'Write the new Install file' },
  { state: 'compose-up', title: 'Start the new version' },
  { state: 'waiting-health', title: 'Wait for the new version to answer' },
];

/** After this long with no answer the page stops saying "Restarting" and names the host commands. */
export const NO_ANSWER_MS = 5 * 60 * 1000;

/** The poll interval while Updating or Restarting. */
export const POLL_MS = 2000;

/**
 * Whether a failed read means the panel is restarting rather than that the
 * read failed: a network error, a 5xx, or Traefik's 404 while it has no route.
 */
export function isRestartingError(reason: unknown): boolean {
  if (!(reason instanceof ApiError)) return true;
  return reason.status >= 500 || reason.status === 404;
}

export function isSessionLost(reason: unknown): boolean {
  return reason instanceof ApiError && reason.status === 401;
}

export type PageState =
  | 'up-to-date'
  | 'available'
  | 'manual'
  | 'preview'
  | 'refused'
  | 'waiting'
  | 'updating'
  | 'restarting'
  | 'no-answer';

/** The page's top-level state. Result alerts for a finished run are separate (`finishedRun`). */
export function pageState(input: {
  status: PanelUpdateStatus;
  report?: PreflightReport;
  restartingMs?: number;
  /** True from the moment this page started or saw a run until that run is over. */
  awaitingRun: boolean;
}): PageState {
  const { status, report, restartingMs, awaitingRun } = input;
  if (restartingMs !== undefined) return restartingMs >= NO_ANSWER_MS ? 'no-answer' : 'restarting';
  if (status.run && !status.run.terminal) return status.run.state === 'quiescing' ? 'waiting' : 'updating';
  if (awaitingRun) return 'updating';
  if (report) {
    if (report.needsManualUpdate) return 'manual';
    if (report.failed || !report.ready) return 'refused';
    return 'preview';
  }
  if (!status.install.available && !status.development && !status.message) return 'manual';
  return status.available ? 'available' : 'up-to-date';
}

/** The newest run when it ended in a way the operator must read. */
export function finishedRun(status: PanelUpdateStatus): PanelUpdateRun | undefined {
  const run = status.run;
  if (!run || !run.terminal) return undefined;
  return run.state === 'rolled-back' ||
    run.state === 'rollback-failed' ||
    run.state === 'interrupted' ||
    run.state === 'aborted'
    ? run
    : undefined;
}

/** Cancels a run that is still waiting for the panel to go quiet. */
export async function cancelUpdate(): Promise<void> {
  await api.panelUpdateCancel();
}

/** The index in `UPDATE_STEPS` a run is at; a rollback stays on the step that failed (the last one). */
export function stepIndex(state: RunState): number {
  const index = UPDATE_STEPS.findIndex((step) => step.state === state);
  return index >= 0 ? index : UPDATE_STEPS.length - 1;
}

/** The data directory a backup path lives in: `<data dir>/.panel-update/<id>`. */
export function dataDirOf(backupPath: string): string {
  const marker = '/.panel-update/';
  const at = backupPath.lastIndexOf(marker);
  return at > 0 ? backupPath.slice(0, at) : '<data dir>';
}

/** What to run on the host to see where an update stopped. */
export function checkCommands(runId: string): string {
  return ['docker ps', `docker logs relist-panel-updater-${runId}`].join('\n');
}

/**
 * What to run on the host to put the backup back by hand. The -wal and -shm
 * files of the database go first: a stale WAL next to the restored file would
 * be replayed over it.
 */
export function restoreCommands(backupPath: string, backupFiles: PanelUpdateBackupFile[] = []): string {
  const data = dataDirOf(backupPath);
  return [
    `ls -la ${backupPath}`,
    'cd <install dir>',
    'docker compose stop panel',
    `rm -f ${data}/panel.db-wal ${data}/panel.db-shm`,
    `cp ${backupPath}/panel.db ${data}/panel.db`,
    ...backupFiles.map((file) => `cp ${backupPath}/${file.copy} ${file.path}`),
    'docker compose up -d',
  ].join('\n');
}

/**
 * The two sides of a unified diff, for a side-by-side view: context and
 * removed lines make the original, context and added lines the modified.
 * Unchanged stretches between hunks are not in the diff, so they are not here.
 */
export function splitUnifiedDiff(diff: string): { original: string; modified: string } {
  const original: string[] = [];
  const modified: string[] = [];
  let inHunk = false;
  for (const line of diff.split('\n')) {
    if (line.startsWith('@@')) {
      inHunk = true;
      continue;
    }
    if (!inHunk || line.startsWith('\\')) continue;
    if (line.startsWith('+')) modified.push(line.slice(1));
    else if (line.startsWith('-')) original.push(line.slice(1));
    else if (line.startsWith(' ')) {
      original.push(line.slice(1));
      modified.push(line.slice(1));
    }
  }
  return { original: original.join('\n'), modified: modified.join('\n') };
}

/** The Update button's gate: the report is clean and every manual step is ticked. */
export function canUpdate(report: PreflightReport, done: string[]): boolean {
  return report.ready && !report.failed && report.manualSteps.every((step) => done.includes(step.id));
}

/**
 * Whole-page navigation, kept behind an object so a test can watch it. A 401
 * goes to sign-in with a real load: the in-app router would bounce a page that
 * still believes it is signed in.
 */
export const navigation = {
  toSignIn: () => window.location.assign('/signin'),
  reload: () => window.location.reload(),
};
