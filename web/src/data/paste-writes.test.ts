import { describe, expect, it } from 'vitest';
import { pasteWrites } from './paste-writes';

const current = [
  { id: 1, key: 'SAME', secret: false, value: 'a' },
  { id: 2, key: 'CHANGED', secret: true, value: 'old' },
  { id: 3, key: 'MISSING', secret: false, value: 'x' },
];
const rows = [
  { key: 'SAME', secret: false, value: 'a' },
  { key: 'CHANGED', secret: true, value: 'new' },
  { key: 'ADDED', secret: false, value: '1' },
];

describe('pasteWrites', () => {
  it('merges by default: creates new keys, updates changed ones, skips unchanged and keeps missing', () => {
    expect(pasteWrites(current, rows, { prune: false })).toEqual({
      creates: [rows[2]],
      deletes: [],
      updates: [{ id: 2, row: rows[1] }],
    });
  });

  it('also deletes the keys the paste drops when pruning', () => {
    expect(pasteWrites(current, rows, { prune: true }).deletes).toEqual([3]);
  });

  it('updates a key whose secret flag changed even when the value did not', () => {
    const flagged = [{ key: 'SAME', secret: true, value: 'a' }];
    expect(pasteWrites(current, flagged, { prune: false }).updates).toEqual([{ id: 1, row: flagged[0] }]);
  });
});
