import { describe, expect, it } from 'vitest';

import type { Builder } from '../data/types';
import { builderStatus, formatLimits } from './builders-view';

const base: Builder = { cpus: 2, id: 1, lastError: '', maxParallelism: 4, memoryMb: 4096, name: 'large', state: 'running' };

describe('formatLimits', () => {
  it('reads like the Deployment log line', () => {
    expect(formatLimits(base)).toBe('large (2 CPU, 4 GB, 4 parallel steps)');
    expect(formatLimits({ ...base, cpus: 0.5, maxParallelism: null, memoryMb: 1536, name: 'a' })).toBe('a (0.5 CPU, 1536 MB)');
    expect(formatLimits({ ...base, cpus: 1.5, maxParallelism: null, memoryMb: 256, name: 'c' })).toBe('c (1.5 CPU, 256 MB)');
  });
});

describe('builderStatus', () => {
  it('names each state and puts an error first', () => {
    expect(builderStatus(base)).toEqual({ color: 'success', label: 'Running' });
    expect(builderStatus({ ...base, state: 'stopped' })).toEqual({ color: 'default', label: 'Stopped' });
    expect(builderStatus({ ...base, state: 'missing' })).toEqual({ color: 'default', label: 'Not created yet' });
    expect(builderStatus({ ...base, state: 'unknown' })).toEqual({ color: 'warning', label: 'Unknown' });
    expect(builderStatus({ ...base, lastError: 'boom' })).toEqual({ color: 'error', label: 'Error' });
  });
});
