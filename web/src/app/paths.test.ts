import { describe, expect, it } from 'vitest';

import { paths, resolvePath } from './paths';

describe('paths', () => {
  // The design's route table (GUIDELINES §3), with the prototype's `#` prefix
  // dropped — history routing is the contract the Go fallback serves.
  it('builds the design route table', () => {
    expect(paths.overview).toBe('/');
    expect(paths.projects).toBe('/projects');
    expect(paths.monitoring).toBe('/monitoring');
    expect(paths.project(1)).toBe('/projects/1');
    expect(paths.environment(1, 2)).toBe('/projects/1/env/2/composes');
    expect(paths.environment(1, 2, 'variables')).toBe('/projects/1/env/2/variables');
    expect(paths.compose(1, 2, 3)).toBe('/projects/1/env/2/compose/3');
    expect(paths.compose(1, 2, 3, 'monitor')).toBe('/projects/1/env/2/compose/3/monitor');
  });
});

describe('resolvePath', () => {
  it('resolves every designed path shape', () => {
    expect(resolvePath('/')).toEqual({ params: {}, pattern: '/' });
    expect(resolvePath('/projects')).toEqual({ params: {}, pattern: 'projects' });
    expect(resolvePath('/projects/1')).toEqual({ params: { projectId: '1' }, pattern: 'projects/:projectId' });
    expect(resolvePath('/projects/1/env/2/variables')).toEqual({
      params: { environmentId: '2', projectId: '1', tab: 'variables' },
      pattern: 'projects/:projectId/env/:environmentId/:tab',
    });
    expect(resolvePath('/projects/1/env/2/compose/3')).toEqual({
      params: { composeId: '3', environmentId: '2', projectId: '1' },
      pattern: 'projects/:projectId/env/:environmentId/compose/:composeId',
    });
    expect(resolvePath('/projects/1/env/2/compose/3/monitor')).toEqual({
      params: { composeId: '3', environmentId: '2', projectId: '1', tab: 'monitor' },
      pattern: 'projects/:projectId/env/:environmentId/compose/:composeId/:tab',
    });
  });

  it('prefers the compose route over the tab route', () => {
    // `compose` is static, so it must out-rank the environment's `:tab`.
    const resolved = resolvePath('/projects/1/env/2/compose/3');
    expect(resolved?.pattern).toBe('projects/:projectId/env/:environmentId/compose/:composeId');
  });

  it('tolerates a trailing slash', () => {
    expect(resolvePath('/projects/1/')?.params).toEqual({ projectId: '1' });
  });

  it('leaves unknown paths to the not-found page', () => {
    expect(resolvePath('/nope')).toBeUndefined();
    expect(resolvePath('/projects/1/compose/3')).toBeUndefined();
  });
});
