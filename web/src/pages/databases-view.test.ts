import { describe, expect, it } from 'vitest';

import { canRepair, pollInterval, snippet, statusMeta } from './databases-view';

describe('pollInterval', () => {
  it('is 3s while a Database is coming up and 15s otherwise', () => {
    expect(pollInterval([{ status: 'running' }, { status: 'creating' }])).toBe(3000);
    expect(pollInterval([{ status: 'starting' }])).toBe(3000);
    expect(pollInterval([{ status: 'running' }, { status: 'stopped' }])).toBe(15000);
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
