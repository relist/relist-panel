import { describe, expect, it } from 'vitest';

import type { Compose, Project } from '../data/types';
import { activeTab, selectEnvironment, visibleProjects } from './project-view';

const project = (overrides: Partial<Project> & { id: number }): Project => ({
  name: `project-${overrides.id}`,
  description: '',
  environmentCount: 0,
  createdAt: '2026-09-01T10:00:00Z',
  lastUpdated: '2026-09-01T10:00:00Z',
  environments: [],
  ...overrides,
});

describe('activeTab', () => {
  it('resolves the known segments', () => {
    expect(activeTab('composes')).toBe('composes');
    expect(activeTab('variables')).toBe('variables');
    expect(activeTab('settings')).toBe('settings');
  });

  it('falls back to Composes for anything else, including an absent segment', () => {
    expect(activeTab('nope')).toBe('composes');
    expect(activeTab(undefined)).toBe('composes');
    expect(activeTab('')).toBe('composes');
  });
});

describe('selectEnvironment', () => {
  const environments = [
    { id: 1, projectId: 1, name: 'production', createdAt: '2026-09-01T10:00:00Z', variables: [], composes: [] },
    { id: 5, projectId: 1, name: 'staging', createdAt: '2026-09-02T10:00:00Z', variables: [], composes: [] },
  ];
  const subject = project({ id: 1, environments });

  it('picks the Environment named by the URL', () => {
    expect(selectEnvironment(subject, 5)?.name).toBe('staging');
  });

  it('falls back to the first Environment on an unknown id (stale URL)', () => {
    expect(selectEnvironment(subject, 99)?.name).toBe('production');
  });

  it('falls back to the first Environment when the URL names none', () => {
    expect(selectEnvironment(subject)?.name).toBe('production');
    expect(selectEnvironment(subject, undefined)?.id).toBe(1);
  });

  it('is undefined for a Project without Environments', () => {
    expect(selectEnvironment(project({ id: 2 }))).toBeUndefined();
  });
});

describe('visibleProjects', () => {
  const list = [
    project({ id: 1, name: 'my-saas', description: 'The customer-facing application', lastUpdated: '2026-09-24T10:00:00Z', environments: [environmentsOf(2)] }),
    project({ id: 2, name: 'edge-relay', description: '', lastUpdated: '2026-09-20T10:00:00Z', environments: [environmentsOf(1), environmentsOf(1)] }),
    project({ id: 3, name: 'homelab', description: 'dns and friends', lastUpdated: '2026-09-25T09:00:00Z' }),
  ];

  function environmentsOf(count: number) {
    return { id: count, projectId: 1, name: 'production', createdAt: '2026-09-01T10:00:00Z', variables: [], composes: [] as Compose[] };
  }

  it('sorts by recently updated by default, then by name or size on request', () => {
    expect(visibleProjects(list, '', 'updated').map((p) => p.id)).toEqual([3, 1, 2]);
    expect(visibleProjects(list, '', 'name').map((p) => p.id)).toEqual([2, 3, 1]);
    expect(visibleProjects(list, '', 'size').map((p) => p.id)).toEqual([2, 1, 3]);
  });

  it('filters by name or description, case-insensitively', () => {
    expect(visibleProjects(list, 'SAAS', 'updated').map((p) => p.id)).toEqual([1]);
    expect(visibleProjects(list, 'dns', 'updated').map((p) => p.id)).toEqual([3]);
  });

  it('trims the query and yields everything for a blank one', () => {
    expect(visibleProjects(list, '   ', 'updated')).toHaveLength(3);
    expect(visibleProjects(list, ' relay ', 'updated').map((p) => p.id)).toEqual([2]);
  });
});
