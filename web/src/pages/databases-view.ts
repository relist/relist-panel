import type { BackupTrigger, Database, DatabaseBackup, DatabaseRestore, DatabaseStatus } from '../data/types';

type Meta = { color: string; label: string };

const STATUS: Record<DatabaseStatus, Meta> = {
  creating: { color: 'processing', label: 'Creating' },
  deleted: { color: 'default', label: 'Deleted' },
  drift: { color: 'orange', label: 'Drift' },
  error: { color: 'error', label: 'Error' },
  running: { color: 'success', label: 'Running' },
  starting: { color: 'processing', label: 'Starting' },
  stopped: { color: 'default', label: 'Stopped' },
  unhealthy: { color: 'error', label: 'Unhealthy' },
  volume_missing: { color: 'error', label: 'Volume missing' },
};

/** The Tag colour and label of a Database's status. */
export function statusMeta(status: DatabaseStatus): Meta {
  return STATUS[status] ?? { color: 'default', label: status };
}

/**
 * Repair offers itself when the container differs from the record (drift),
 * failed (error), or is stopped though the Admin wants it running. A
 * Database stopped on purpose has nothing to repair, and a missing volume is
 * not repairable: nothing is recreated for it.
 */
export function canRepair(db: Pick<Database, 'status' | 'desired'>): boolean {
  if (db.status === 'drift' || db.status === 'error') return true;
  return db.status === 'stopped' && db.desired === 'running';
}

/** How often to reload: fast while something is coming up, slow otherwise. */
export function pollInterval(rows: Pick<Database, 'status' | 'job'>[] | undefined): number {
  if (rows?.some((row) => row.job)) return 2000;
  return rows?.some((row) => row.status === 'creating' || row.status === 'starting') ? 3000 : 15000;
}

/** The lines to paste into a Compose service so it reaches the Database. */
export function snippet(url: string): string {
  return `environment:\n  DATABASE_URL: ${url}\nnetworks: [relist-network]`;
}

const TRIGGERS: Record<BackupTrigger, string> = {
  manual: 'Manual',
  pre_restore: 'Before restore',
  pre_update: 'Before update',
};

/** The Tag text of what started a Backup. */
export function triggerLabel(trigger: BackupTrigger): string {
  return TRIGGERS[trigger] ?? trigger;
}

/** A size in 1024-based units, one decimal at most: 1536 is "1.5 KB". */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1).replace(/\.0$/, '')} ${units[unit]}`;
}

/** Reload fast while a Backup, a restore or a job runs; slowly otherwise. */
export function backupsPollInterval(
  db: Pick<Database, 'job'> | undefined,
  backups: Pick<DatabaseBackup, 'status'>[] | undefined,
  restores: Pick<DatabaseRestore, 'status'>[] | undefined,
): number {
  const running = (rows: { status: string }[] | undefined) => rows?.some((row) => row.status === 'running');
  return db?.job || running(backups) || running(restores) ? 2000 : 15000;
}

/** A Backup needs a running container: Start the Database first. */
export function canBackUp(db: Pick<Database, 'status' | 'engine' | 'deleted'>): boolean {
  return db.engine !== 'redis' && !db.deleted && (db.status === 'running' || db.status === 'starting' || db.status === 'unhealthy');
}

/** The server's rule for a new restore target: letters, digits, _ and -, 63 at most, not starting with -. */
export function validTargetName(name: string): boolean {
  return /^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$/.test(name);
}

/** What the restore dialog says about its target. */
export function restoreWarning(target: string, replace: boolean): string {
  return replace
    ? `Replaces the database ${target}. It is backed up first. Apps connected to it are disconnected.`
    : `Creates the database ${target}.`;
}

/** The line under the header while a job runs. */
export function jobNotice(job: Database['job']): string {
  switch (job) {
    case 'backup':
      return 'A backup is running. Start, Stop, Restart, Update image, Apply settings and Delete wait until it ends.';
    case 'restore':
      return 'A restore is running. Start, Stop, Restart, Update image, Apply settings and Delete wait until it ends.';
    case 'update_image':
      return 'An image update is running: it backs up first, then updates. Other changes wait until it ends.';
    default:
      return '';
  }
}
