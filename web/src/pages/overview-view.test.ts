import { describe, expect, it } from 'vitest';

import type { Compose, ComposeService, Project } from '../data/types';
import { attentionRows, composeComparators, degradationNotes, flattenComposes, needsAttention, overviewStats, withDeployStamps } from './overview-view';

let nextId = 1;

const service = (overrides: Partial<ComposeService> & { name: string }): ComposeService => ({
  image: `${overrides.name}:1.0`,
  state: 'running',
  health: 'none',
  ports: [],
  ...overrides,
});

const compose = (overrides: Partial<Compose> & { id?: number; name: string }): Compose => ({
  id: nextId++,
  environmentId: 1,
  projectSlug: 'my-saas',
  envSlug: 'production',
  yamlContent: '',
  createdAt: '2026-09-20T10:00:00Z',
  updatedAt: '2026-09-24T10:00:00Z',
  status: { running: 2, total: 2 },
  ...overrides,
});

const environment = (id: number, name: string, composes: Compose[], projectId = 1) => ({
  id,
  projectId,
  name,
  createdAt: '2026-09-20T10:00:00Z',
  variables: [],
  composes,
});

const project = (id: number, name: string, environments: ReturnType<typeof environment>[]): Project => ({
  id,
  name,
  description: '',
  environmentCount: environments.length,
  createdAt: '2026-09-20T10:00:00Z',
  lastUpdated: '2026-09-24T10:00:00Z',
  environments,
});

const tree = [
  project(1, 'my-saas', [
    environment(1, 'production', [
      compose({ id: 1, name: 'web', updatedAt: '2026-09-25T06:00:00Z', lastDeployedAt: '2026-09-25T07:30:00Z' }),
      compose({ id: 2, name: 'api', updatedAt: '2026-09-24T10:00:00Z', status: { running: 1, total: 2 } }),
    ]),
    environment(2, 'staging', [compose({ id: 3, name: 'worker', updatedAt: '2026-09-23T10:00:00Z', status: { running: 0, total: 3 } })]),
  ]),
  project(2, 'homelab', [environment(3, 'dns', [compose({ id: 4, name: 'bind', updatedAt: '2026-09-22T10:00:00Z', status: { running: 0, total: 0 } })], 2)]),
];

describe('flattenComposes', () => {
  it('flattens the tree into rows carrying the Project / Environment trail', () => {
    const flat = flattenComposes(tree);
    expect(flat).toHaveLength(4);
    expect(flat.map((row) => row.compose.id)).toEqual([1, 2, 3, 4]);
    expect(flat[2]).toMatchObject({ environmentName: 'staging', projectName: 'my-saas', projectId: 1, environmentId: 2 });
    expect(flat[3]).toMatchObject({ environmentName: 'dns', projectName: 'homelab' });
  });

  it('is empty when there are no Projects', () => {
    expect(flattenComposes([])).toEqual([]);
  });
});

describe('withDeployStamps', () => {
  it('fills an absent lastDeployedAt from the store stamp and lets the API win', () => {
    const stamped = withDeployStamps(tree, { 4: '2026-09-25T08:00:00Z' });
    const apiDated = stamped[0].environments[0].composes.find((compose) => compose.id === 1);
    const sessionDated = stamped[1].environments[0].composes.find((compose) => compose.id === 4);
    const untouched = stamped[0].environments[0].composes.find((compose) => compose.id === 2);
    expect(apiDated?.lastDeployedAt).toBe('2026-09-25T07:30:00Z');
    expect(sessionDated?.lastDeployedAt).toBe('2026-09-25T08:00:00Z');
    expect(untouched?.lastDeployedAt).toBeUndefined();
  });

  it('raises a compose stopped after a session-stamped deploy onto the attention list', () => {
    const flat = flattenComposes(withDeployStamps(tree, { 3: '2026-09-25T08:00:00Z' }));
    expect(attentionRows(flat).map((row) => row.compose.id)).toEqual([2, 3]);
  });
});

describe('overviewStats', () => {
  it('counts the four cards across the whole tree', () => {
    expect(overviewStats(tree)).toEqual({
      projects: 2,
      environments: 3,
      composes: 4,
      running: 3,
      containers: 7,
      neverDeployed: 1,
    });
  });

  it('is all zeros for an empty panel', () => {
    expect(overviewStats([])).toEqual({ composes: 0, containers: 0, environments: 0, neverDeployed: 0, projects: 0, running: 0 });
  });
});

describe('needsAttention', () => {
  it('raises partly running composes', () => {
    expect(needsAttention(compose({ name: 'x', status: { running: 1, total: 3 } }))).toBe(true);
  });

  it('raises stopped-after-success, but not never-deployed composes', () => {
    expect(needsAttention(compose({ name: 'x', status: { running: 0, total: 3 } }))).toBe(true);
    expect(needsAttention(compose({ name: 'x', status: { running: 0, total: 0 } }))).toBe(false);
    expect(needsAttention(compose({ name: 'x', status: { running: 0, total: 0 }, lastDeployedAt: '2026-09-25T07:00:00Z' }))).toBe(true);
  });

  it('leaves running composes alone', () => {
    expect(needsAttention(compose({ name: 'x', status: { running: 2, total: 2 } }))).toBe(false);
  });

  it('raises an unhealthy service even while the counts look fine', () => {
    expect(
      needsAttention(compose({ name: 'x' }), [service({ name: 'web', health: 'unhealthy' })]),
    ).toBe(true);
  });

  it('decides from the counts when no service rows have been loaded', () => {
    expect(needsAttention(compose({ name: 'x', status: { running: 1, total: 2 } }))).toBe(true);
    expect(needsAttention(compose({ name: 'y', status: { running: 2, total: 2 } }))).toBe(false);
    expect(needsAttention(compose({ name: 'y', status: { running: 2, total: 2 } }), [])).toBe(false);
    expect(degradationNotes(compose({ name: 'x', status: { running: 1, total: 2 } }))).toEqual([]);
  });
});

describe('attentionRows', () => {
  it('keeps one row per degraded Compose, in tree order', () => {
    const flat = flattenComposes(tree);
    expect(attentionRows(flat).map((row) => row.compose.id)).toEqual([2, 3]);
  });

  it('raises a compose whose loaded services name an unhealthy one', () => {
    const flat = flattenComposes(tree);
    const services = { 1: [service({ name: 'web', health: 'unhealthy' })] };
    expect(attentionRows(flat, services).map((row) => row.compose.id)).toEqual([1, 2, 3]);
  });
});

describe('degradationNotes', () => {
  it('names exited and unhealthy services, then tells the stopped story', () => {
    const subject = compose({ name: 'web', status: { running: 0, total: 2 } });
    const rows = [service({ name: 'web', state: 'exited' }), service({ name: 'api', health: 'unhealthy' })];
    expect(degradationNotes(subject, rows)).toEqual(['web exited', 'api unhealthy', 'Stopped after a successful deploy']);
  });

  it('says nothing extra for a partly running compose with no service detail', () => {
    expect(degradationNotes(compose({ name: 'api', status: { running: 1, total: 2 } }))).toEqual([]);
  });

  it('notes the stop for a compose that has never had containers but was deployed', () => {
    expect(degradationNotes(compose({ name: 'x', status: { running: 0, total: 0 }, lastDeployedAt: '2026-09-25T07:00:00Z' }))).toEqual([
      'Stopped after a successful deploy',
    ]);
  });
});

describe('composeComparators', () => {
  const flat = flattenComposes(tree);
  const pick = (rows: ReturnType<typeof flattenComposes>) => rows.map((row) => row.compose.id);

  it('sorts by Compose name', () => {
    expect(pick([...flat].sort(composeComparators.compose))).toEqual([2, 4, 1, 3]);
  });

  it('sorts by Project / Environment trail, then name within it', () => {
    expect(pick([...flat].sort(composeComparators.where))).toEqual([4, 2, 1, 3]);
  });

  it('sorts ascending by containers; the column flips it to most-running-first', () => {
    expect(pick([...flat].sort(composeComparators.containers))).toEqual([4, 3, 2, 1]);
  });

  it('sorts ascending by last edited; the column opens newest-first', () => {
    expect(pick([...flat].sort(composeComparators.updated))).toEqual([4, 3, 2, 1]);
  });

  it('sorts ascending by last deployed, never-deployed first; descend puts them last', () => {
    const rows = [
      { ...flat[0], compose: { ...flat[0].compose, lastDeployedAt: '2026-09-25T07:30:00Z' } },
      { ...flat[1], compose: { ...flat[1].compose, lastDeployedAt: '2026-09-25T09:00:00Z' } },
      { ...flat[2], compose: { ...flat[2].compose, lastDeployedAt: undefined } },
      { ...flat[3], compose: { ...flat[3].compose, lastDeployedAt: undefined } },
    ];
    expect(pick([...rows].sort(composeComparators.deployed))).toEqual([4, 3, 1, 2]);
  });
});
