import { describe, expect, it } from 'vitest';

import type { EffectiveVariable } from '../data/types';
import { bindingRows, effectiveRows, entryCountLabel, setName, sourceKind, sourceLabel, usageLabel } from './bindings-view';

describe('bindingRows', () => {
  it('gives every YAML Service a row, in YAML order, bound or not', () => {
    const { live, stale } = bindingRows(['web', 'db', 'cache'], [{ service: 'db', setIds: [3, 1] }]);
    expect(live).toEqual([
      { service: 'web', setIds: [] },
      { service: 'db', setIds: [3, 1] },
      { service: 'cache', setIds: [] },
    ]);
    expect(stale).toEqual([]);
  });

  it('keeps binding order, which is the order of the ids', () => {
    expect(bindingRows(['web'], [{ service: 'web', setIds: [9, 2, 5] }]).live[0].setIds).toEqual([9, 2, 5]);
  });

  it('puts bindings for Services missing from the YAML in stale', () => {
    const { live, stale } = bindingRows(['web'], [
      { service: 'web', setIds: [1] },
      { service: 'worker', setIds: [2] },
    ]);
    expect(live.map((row) => row.service)).toEqual(['web']);
    expect(stale).toEqual([{ service: 'worker', setIds: [2] }]);
  });

  it('does not report an empty binding of a removed Service as stale', () => {
    expect(bindingRows([], [{ service: 'gone', setIds: [] }]).stale).toEqual([]);
  });

  it('has nothing at all for a Compose without Services or bindings', () => {
    expect(bindingRows([], [])).toEqual({ live: [], stale: [] });
  });
});

describe('labels', () => {
  it('looks a set up by id and falls back for an unknown one', () => {
    expect(setName([{ id: 1, name: 'db' }], 1)).toBe('db');
    expect(setName([{ id: 1, name: 'db' }], 4)).toBe('set #4');
  });

  it('writes compose/service and singular or plural entries', () => {
    expect(usageLabel({ composeName: 'shop', service: 'web' })).toBe('shop/web');
    expect(entryCountLabel(1)).toBe('1 entry');
    expect(entryCountLabel(0)).toBe('0 entries');
    expect(entryCountLabel(3)).toBe('3 entries');
  });

  it('labels each source layer', () => {
    expect(sourceLabel('environment')).toBe('Environment');
    expect(sourceLabel('compose')).toBe('Compose');
    expect(sourceLabel('yaml')).toBe('set in YAML, wins');
    expect(sourceLabel({ set: { id: 2, name: 'db' } })).toBe('db');
    expect(sourceKind({ set: { id: 2, name: 'db' } })).toBe('set');
    expect(sourceKind('compose')).toBe('compose');
  });
});

describe('effectiveRows', () => {
  const row = (key: string, source: EffectiveVariable['source'], overrides: EffectiveVariable['overrides'] = []): EffectiveVariable => ({
    key,
    overrides,
    secret: false,
    source,
    value: source === 'yaml' ? '' : 'v',
  });

  it('marks a merged row whose key the YAML also sets', () => {
    const rows = effectiveRows([row('A', 'compose', ['environment', 'yaml']), row('B', 'environment'), row('A', 'yaml')]);
    expect(rows.map((r) => [r.key, r.source, r.overriddenByYaml])).toEqual([
      ['A', 'compose', true],
      ['B', 'environment', false],
      ['A', 'yaml', false],
    ]);
  });

  it('keeps the shadowed layers but not the yaml mark among them', () => {
    const [plain, marked] = effectiveRows([
      row('A', { set: { id: 1, name: 'x' } }, ['environment', 'compose']),
      row('B', 'compose', ['environment', 'yaml']),
    ]);
    expect(plain.overrides).toEqual(['environment', 'compose']);
    expect(plain.overriddenByYaml).toBe(false);
    expect(marked.overrides).toEqual(['environment']);
  });
});
