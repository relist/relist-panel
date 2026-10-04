import { describe, expect, it } from 'vitest';

import type { ComposeVolumes } from '../data/types';
import { activeComposeTab, composeDeployIdentity, composeLinkIsStale, deploySteps, filterLogLines, freshDeployView, hasUnsavedChanges, logServiceOptions } from './compose-view';

describe('composeDeployIdentity', () => {
  it('uses the API identity including the configured absolute directory for new Composes', () => {
    const actual = { projectName: 'relist-c42', directory: '/srv/custom panel/.composes/42' };
    expect(composeDeployIdentity({ projectSlug: 'old-name', envSlug: 'production', name: 'Web App', deployIdentity: actual })).toEqual(actual);
  });

  it('preserves the resolved API legacy identity rather than recomputing it', () => {
    const actual = { projectName: 'original-production-web', directory: '/opt/stacks/original/production/web' };
    expect(composeDeployIdentity({ projectSlug: 'renamed', envSlug: 'production', name: 'web', deployIdentity: actual })).toEqual(actual);
  });

  it('falls back to the legacy slug identity only when older API data omits the field', () => {
    expect(composeDeployIdentity({ projectSlug: 'project', envSlug: 'production', name: ' Web App! ' })).toEqual({
      projectName: 'project-production-web-app', directory: 'dataDir/project/production/web-app',
    });
    expect(composeDeployIdentity({ projectSlug: 'project', envSlug: 'production', name: 'ภาษาไทย' }).projectName).toBe('project-production-x');
  });
});

describe('activeComposeTab', () => {
  it('resolves the tab segment the design links to', () => {
    expect(activeComposeTab('monitor')).toBe('monitor');
    expect(activeComposeTab('logs')).toBe('logs');
    expect(activeComposeTab('volumes')).toBe('volumes');
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

  it('matches a Service name against its container labels, not a bare prefix', () => {
    const mixed = 'web-1  | a\nweb-2  | b\nweb2-1 | c';
    expect(filterLogLines(mixed, ['web'], 200)).toEqual(['web-1  | a', 'web-2  | b']);
  });
});

describe('logServiceOptions', () => {
  const logs = 'web-1  | a\ndb-1   | b\nweb-1  | c';

  it('names the Services the log text mentions when the collector knows none', () => {
    expect(logServiceOptions(logs, [])).toEqual(['db', 'web']);
  });

  it('does not repeat a known Service the logs also name', () => {
    expect(logServiceOptions(logs, ['web'])).toEqual(['db', 'web']);
  });

  it('offers the known Services before any logs are fetched', () => {
    expect(logServiceOptions(undefined, ['web'])).toEqual(['web']);
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

describe('freshDeployView', () => {
  const volume = (source: string, kind: 'volume' | 'bind' = 'volume') => ({ source, kind }) as ComposeVolumes['volumes'][number];
  const payload = (volumes: ComposeVolumes['volumes'], available = true): ComposeVolumes => ({ available, volumes, services: [] });

  it('lists the named volumes of the deployed file and allows the deploy', () => {
    const view = freshDeployView(payload([volume('demo_data'), volume('/srv/bind', 'bind'), volume('demo_cache')]), undefined);
    expect(view).toEqual({ volumes: ['demo_data', 'demo_cache'], refusal: undefined });
  });

  it('allows the deploy with an empty list when the file has no named volumes', () => {
    expect(freshDeployView(payload([]), undefined)).toEqual({ volumes: [], refusal: undefined });
  });

  it('says the Compose has not been deployed only for the before-first-deploy error', () => {
    const view = freshDeployView(undefined, "volume resolution failed: couldn't find env file: /data/.env");
    expect(view.refusal).toMatch(/has not been deployed yet/);
    expect(view.volumes).toEqual([]);
  });

  it('reports any other resolution failure as unresolved, with its message', () => {
    const view = freshDeployView(undefined, 'docker unavailable');
    expect(view.refusal).toBe('This Compose’s volumes could not be resolved: docker unavailable');
    expect(view.refusal).not.toMatch(/not been deployed/);
  });

  it('refuses while the resolution is in flight and when volume management is unavailable', () => {
    expect(freshDeployView(undefined, undefined).refusal).toMatch(/Resolving/);
    expect(freshDeployView(payload([], false), undefined).refusal).toBe('Volume management is unavailable on this host.');
  });
});
