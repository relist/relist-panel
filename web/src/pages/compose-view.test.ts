import { describe, expect, it } from 'vitest';

import { activeComposeTab, composeLinkIsStale, deploySteps, filterLogLines, hasUnsavedChanges } from './compose-view';

describe('activeComposeTab', () => {
  it('resolves the tab segment the design links to', () => {
    expect(activeComposeTab('monitor')).toBe('monitor');
    expect(activeComposeTab('editor')).toBe('editor');
  });

  it('falls back to Editor for anything else, including an absent segment', () => {
    expect(activeComposeTab('nope')).toBe('editor');
    expect(activeComposeTab(undefined)).toBe('editor');
    expect(activeComposeTab('')).toBe('editor');
  });
});

describe('hasUnsavedChanges', () => {
  it('is false while the draft matches the saved file', () => {
    expect(hasUnsavedChanges('services: {}\n', 'services: {}\n')).toBe(false);
  });

  it('is true for any divergence, including whitespace-only edits', () => {
    expect(hasUnsavedChanges('services: {}\n', 'services: {}\r\n')).toBe(true);
    expect(hasUnsavedChanges('', 'services: {}\n')).toBe(true);
  });
});

describe('deploySteps', () => {
  it('saves a dirty file before the deploy reaches the host', () => {
    expect(deploySteps(true)).toEqual(['save', 'deploy']);
  });

  it('deploys a clean file without a redundant save', () => {
    expect(deploySteps(false)).toEqual(['deploy']);
  });
});

describe('filterLogLines', () => {
  const logs = [
    'web-1  | listening on 8080',
    'db-1   | checkpoint complete',
    'web-1  | GET /healthz 200',
    'db-1   | autovacuum launch',
  ].join('\n');

  it('returns nothing when nothing was fetched', () => {
    expect(filterLogLines(undefined, [], 200)).toEqual([]);
    expect(filterLogLines('', [], 200)).toEqual([]);
  });

  it('keeps every line when no service is selected, trimmed to the last N', () => {
    expect(filterLogLines(logs, [], 2)).toEqual(['web-1  | GET /healthz 200', 'db-1   | autovacuum launch']);
    expect(filterLogLines(logs, [], 200)).toHaveLength(4);
  });

  it('keeps only the lines of the selected services, still trimmed to the last N', () => {
    expect(filterLogLines(logs, ['web-1'], 1)).toEqual(['web-1  | GET /healthz 200']);
    expect(filterLogLines(logs, ['web-1', 'db-1'], 200)).toHaveLength(4);
  });

  it('matches on the line prefix after leading whitespace', () => {
    const padded = '  web-1  | indented prefix\nother line';
    expect(filterLogLines(padded, ['web-1'], 200)).toEqual(['  web-1  | indented prefix']);
    expect(filterLogLines(padded, ['eb-1'], 200)).toEqual([]);
  });

  it('yields an empty list when the filter matches nothing', () => {
    expect(filterLogLines(logs, ['cache-1'], 200)).toEqual([]);
  });
});

describe('composeLinkIsStale', () => {
  const location = {
    project: { id: 1 },
    environment: { id: 2 },
    compose: { id: 3 },
  } as Parameters<typeof composeLinkIsStale>[0];

  it('is stale when the URL names parents the Compose does not live in', () => {
    expect(composeLinkIsStale(location, 9, 2)).toBe(true);
    expect(composeLinkIsStale(location, 1, 9)).toBe(true);
  });

  it('is not stale while the URL matches the Compose’s actual parents', () => {
    expect(composeLinkIsStale(location, 1, 2)).toBe(false);
  });

  it('is never stale for an unknown Compose — that is a not-found, not a redirect', () => {
    expect(composeLinkIsStale(undefined, 1, 2)).toBe(false);
  });
});
