import { describe, expect, it } from 'vitest';

import {
  composeState,
  findCompose,
  findComposeLocation,
  findEnvironmentLocation,
  projectComposes,
  summarize,
} from './types';
import type { Compose, Project } from './types';

function compose(overrides: Partial<Compose> & { id: number }): Compose {
  return {
    environmentId: 1,
    name: `compose-${overrides.id}`,
    projectSlug: 'my-saas',
    envSlug: 'production',
    yamlContent: 'services: {}',
    createdAt: '2026-09-01T10:00:00Z',
    updatedAt: '2026-09-24T10:00:00Z',
    status: { running: 0, total: 0 },
    services: [],
    ...overrides,
  };
}

function project(input: { id: number; composes: Compose[]; environments?: Project['environments'] }): Project {
  const { composes, environments, ...rest } = input;
  return {
    name: `project-${input.id}`,
    description: '',
    environmentCount: 1,
    createdAt: '2026-09-01T10:00:00Z',
    lastUpdated: '2026-09-24T10:00:00Z',
    environments:
      environments ?? [
        {
          id: 1,
          projectId: input.id,
          name: 'production',
          createdAt: '2026-09-01T10:00:00Z',
          variables: [],
          composes,
        },
      ],
    ...rest,
  };
}

describe('composeState', () => {
  it.each([
    [{ running: 0, total: 0 }, undefined, 'not-deployed'],
    [{ running: 0, total: 0 }, '2026-01-01T00:00:00Z', 'stopped'],
    [{ running: 0, total: 2 }, undefined, 'stopped'],
    [{ running: 2, total: 2 }, undefined, 'running'],
    [{ running: 1, total: 2 }, undefined, 'partial'],
  ] as const)('%o with lastDeployedAt %s is %s', (status, lastDeployedAt, expected) => {
    expect(composeState(status, lastDeployedAt)).toBe(expected);
  });
});

describe('summarize', () => {
  it('is zero for an empty list', () => {
    expect(summarize([])).toEqual({ running: 0, total: 0, composes: 0 });
  });

  it('aggregates running, total and the Compose count', () => {
    const composes = [
      compose({ id: 1, status: { running: 2, total: 2 } }),
      compose({ id: 2, status: { running: 1, total: 3 } }),
      compose({ id: 3, status: { running: 0, total: 0 } }),
    ];
    expect(summarize(composes)).toEqual({ running: 3, total: 5, composes: 3 });
  });
});

describe('projectComposes', () => {
  it('flattens the Composes of every Environment', () => {
    const first = compose({ id: 1 });
    const second = compose({ id: 2 });
    const third = compose({ id: 3 });
    const candidate = project({
      id: 7,
      composes: [first],
      environments: [
        { id: 1, projectId: 7, name: 'production', createdAt: '', variables: [], composes: [first] },
        { id: 2, projectId: 7, name: 'staging', createdAt: '', variables: [], composes: [second, third] },
      ],
    });
    expect(projectComposes(candidate)).toEqual([first, second, third]);
  });
});
describe('findCompose / findComposeLocation', () => {
  const first = compose({ id: 1 });
  const nested = project({ id: 7, composes: [first] });

  it('finds a Compose and its full location trail', () => {
    const location = findComposeLocation([nested], 1);
    expect(location?.project.id).toBe(7);
    expect(location?.environment.id).toBe(1);
    expect(location?.compose.id).toBe(1);
    expect(findCompose([nested], 1)).toBe(first);
  });

  it('returns undefined for an unknown Compose', () => {
    expect(findComposeLocation([nested], 99)).toBeUndefined();
    expect(findCompose([nested], 99)).toBeUndefined();
  });
});

describe('findEnvironmentLocation', () => {
  const first = compose({ id: 1 });
  const nested = project({
    id: 7,
    composes: [first],
    environments: [
      { id: 1, projectId: 7, name: 'production', createdAt: '', variables: [], composes: [first] },
      { id: 2, projectId: 7, name: 'staging', createdAt: '', variables: [], composes: [] },
    ],
  });

  it('finds an Environment and its Project', () => {
    const location = findEnvironmentLocation([nested], 2);
    expect(location?.project.id).toBe(7);
    expect(location?.environment.name).toBe('staging');
  });

  it('returns undefined for an unknown Environment', () => {
    expect(findEnvironmentLocation([nested], 99)).toBeUndefined();
  });
});
