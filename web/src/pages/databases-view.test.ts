import { describe, expect, it } from 'vitest';

import {
  backupsPollInterval,
  canBackUp,
  canRepair,
  formatBytes,
  jobNotice,
  pollInterval,
  restoreWarning,
  scopeLabel,
  snippet,
  statusMeta,
  triggerLabel,
} from './databases-view';

describe('pollInterval', () => {
  it('is 3s while a Database is coming up and 15s otherwise', () => {
    expect(pollInterval([{ status: 'running', job: '' }, { status: 'creating', job: '' }])).toBe(3000);
    expect(pollInterval([{ status: 'starting', job: '' }])).toBe(3000);
    expect(pollInterval([{ status: 'running', job: '' }, { status: 'stopped', job: '' }])).toBe(15000);
    expect(pollInterval([])).toBe(15000);
    expect(pollInterval(undefined)).toBe(15000);
  });
});

describe('canRepair', () => {
  it('offers Repair for drift, error and a stopped Database that should run', () => {
    expect(canRepair({ status: 'drift', desired: 'running' })).toBe(true);
    expect(canRepair({ status: 'error', desired: 'running' })).toBe(true);
    expect(canRepair({ status: 'stopped', desired: 'running' })).toBe(true);
  });
  it('does not for running, stopped on purpose, or a missing volume', () => {
    expect(canRepair({ status: 'running', desired: 'running' })).toBe(false);
    expect(canRepair({ status: 'stopped', desired: 'stopped' })).toBe(false);
    expect(canRepair({ status: 'volume_missing', desired: 'running' })).toBe(false);
  });
});

describe('snippet', () => {
  it('is the Compose lines for the URL', () => {
    expect(snippet('postgres://u:p@relist-db-1:5432/postgres')).toBe(
      'environment:\n  DATABASE_URL: postgres://u:p@relist-db-1:5432/postgres\nnetworks: [relist-network]',
    );
  });
});

describe('statusMeta', () => {
  it('labels every status', () => {
    expect(statusMeta('volume_missing').label).toBe('Volume missing');
    expect(statusMeta('running').color).toBe('success');
  });
});

describe('pollInterval with a job', () => {
  it('is 2s while a job runs', () => {
    expect(pollInterval([{ status: 'running', job: 'backup' }])).toBe(2000);
  });
});

describe('backup labels', () => {
  it('names the trigger and the scope', () => {
    expect(triggerLabel('manual')).toBe('Manual');
    expect(triggerLabel('pre_restore')).toBe('Before restore');
    expect(triggerLabel('pre_update')).toBe('Before update');
    expect(scopeLabel({ all: true })).toBe('Whole instance');
    expect(scopeLabel({ databases: ['a', 'b'] })).toBe('a, b');
  });
});

describe('formatBytes', () => {
  it('uses 1024-based units', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(2048)).toBe('2 KB');
    expect(formatBytes(5 * 1024 * 1024)).toBe('5 MB');
  });
});

describe('backupsPollInterval', () => {
  it('is 2s while a Backup, a restore or a job runs', () => {
    expect(backupsPollInterval({ job: '' }, [{ status: 'running' }], [])).toBe(2000);
    expect(backupsPollInterval({ job: '' }, [], [{ status: 'running' }])).toBe(2000);
    expect(backupsPollInterval({ job: 'update_image' }, [], [])).toBe(2000);
    expect(backupsPollInterval({ job: '' }, [{ status: 'succeeded' }], [{ status: 'failed' }])).toBe(15000);
    expect(backupsPollInterval(undefined, undefined, undefined)).toBe(15000);
  });
});

describe('restoreWarning', () => {
  it('lists the databases of a selected scope', () => {
    expect(restoreWarning({ databases: ['a', 'b'] })).toEqual([
      'Replaces these databases: a, b. Other databases are untouched.',
      'A backup of the current state is taken first. Apps connected to the affected databases are disconnected.',
    ]);
  });
  it('says the whole instance for a whole-instance Backup and an upload', () => {
    expect(restoreWarning({ all: true })[0]).toBe('Replaces the whole instance.');
    expect(restoreWarning('upload')[0]).toBe('Replaces the whole instance.');
  });
});

describe('canBackUp', () => {
  it('needs a running SQL Database', () => {
    expect(canBackUp({ status: 'running', engine: 'postgres', deleted: false })).toBe(true);
    expect(canBackUp({ status: 'stopped', engine: 'postgres', deleted: false })).toBe(false);
    expect(canBackUp({ status: 'running', engine: 'redis', deleted: false })).toBe(false);
    expect(canBackUp({ status: 'deleted', engine: 'postgres', deleted: true })).toBe(false);
  });
});

describe('jobNotice', () => {
  it('names the job', () => {
    expect(jobNotice('backup')).toContain('backup is running');
    expect(jobNotice('restore')).toContain('restore is running');
    expect(jobNotice('update_image')).toContain('image update');
    expect(jobNotice('')).toBe('');
  });
});
