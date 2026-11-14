import type { Database, DatabaseStatus } from '../data/types';

type Meta = { color: string; label: string };

const STATUS: Record<DatabaseStatus, Meta> = {
  creating: { color: 'processing', label: 'Creating' },
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
export function pollInterval(rows: Pick<Database, 'status'>[] | undefined): number {
  return rows?.some((row) => row.status === 'creating' || row.status === 'starting') ? 3000 : 15000;
}

/** The lines to paste into a Compose service so it reaches the Database. */
export function snippet(url: string): string {
  return `environment:\n  DATABASE_URL: ${url}\nnetworks: [relist-network]`;
}
