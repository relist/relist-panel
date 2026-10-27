import { describe, expect, it } from 'vitest';

import type { ScannerTag, VulnImage, VulnImageUser, VulnStatus, VulnSummary } from '../data/types';
import {
  cveQueryFromSearch,
  cveQueryToSearch,
  dbFreshness,
  parseDailyTime,
  scanRowState,
  summaryBadge,
  tagChoiceNeedsConfirm,
  usedByLabel,
  vulnLink,
} from './vulnerabilities-view';

const counts = { critical: 2, high: 5, medium: 1, low: 0, unknown: 0, fixable: 3 };

describe('summaryBadge', () => {
  it('hides when scanning is off or the summary has not loaded', () => {
    expect(summaryBadge(undefined)).toBeNull();
    expect(summaryBadge({ enabled: false })).toBeNull();
  });
  it('shows critical, high and fixable', () => {
    expect(summaryBadge({ enabled: true, counts } as VulnSummary)).toEqual({ critical: 2, high: 5, fixable: 3 });
  });
});

describe('usedByLabel', () => {
  const names = new Map([[3, { projectName: 'shop', environmentName: 'prod', composeName: 'web' }]]);
  it.each<[VulnImageUser, string]>([
    [{ kind: 'service', composeId: 3, service: 'api' }, 'api (shop / prod / web)'],
    [{ kind: 'service', composeId: 99, service: 'api' }, 'api'],
    [{ kind: 'panel' }, 'Panel'],
    [{ kind: 'traefik' }, 'Traefik'],
    [{ kind: 'scanner' }, 'Scanner (Trivy)'],
  ])('%o', (user, label) => {
    expect(usedByLabel(user, names)).toBe(label);
  });
});

describe('dbFreshness', () => {
  const status = (db: Partial<VulnStatus['db']>): VulnStatus => ({
    enabled: true, running: null, queued: 0, lastDailyAt: null, nextDailyAt: null, timeZone: 'UTC (UTC+00:00)', cacheBytes: 0,
    db: { version: '', updatedAt: null, stale: false, error: '', ...db },
  });
  it('is missing before the first download', () => {
    expect(dbFreshness(status({}))).toBe('missing');
  });
  it('follows the server stale flag', () => {
    expect(dbFreshness(status({ version: 'v', updatedAt: '2026-10-20T00:00:00Z', stale: true }))).toBe('stale');
    expect(dbFreshness(status({ version: 'v', updatedAt: '2026-10-27T00:00:00Z' }))).toBe('ok');
  });
  it('is stale when the last update failed even with a DB', () => {
    expect(dbFreshness(status({ version: 'v', updatedAt: '2026-10-27T00:00:00Z', error: 'net', stale: true }))).toBe('stale');
  });
});

describe('scanRowState', () => {
  const image = (over: Partial<VulnImage>): VulnImage => ({
    imageId: 'sha256:a', refs: ['a:1'], users: [], os: '', counts: null, scannedAt: null, attemptedAt: null, error: '', ...over,
  });
  it.each<[Partial<VulnImage>, string]>([
    [{}, 'never'],
    [{ counts, scannedAt: '2026-10-27T00:00:00Z' }, 'ok'],
    [{ error: 'boom', attemptedAt: '2026-10-27T00:00:00Z' }, 'failed'],
    [{ counts, scannedAt: '2026-10-26T00:00:00Z', error: 'boom' }, 'failedWithResults'],
  ])('%o', (over, state) => {
    expect(scanRowState(image(over))).toBe(state);
  });
});

describe('cveQueryFromSearch', () => {
  it('reads filters and ignores junk', () => {
    const q = cveQueryFromSearch(new URLSearchParams('projectId=4&composeId=x&severity=CRITICAL,bogus,HIGH&fixable=1&new=1&q=ssl&page=2&pageSize=-3'));
    expect(q).toEqual({ projectId: 4, severity: ['CRITICAL', 'HIGH'], fixable: true, new: true, q: 'ssl', page: 2, pageSize: 20 });
  });
  it('defaults', () => {
    expect(cveQueryFromSearch(new URLSearchParams())).toEqual({ severity: [], fixable: false, new: false, q: '', page: 1, pageSize: 20 });
  });
  it('round-trips through the search string', () => {
    const q = cveQueryFromSearch(new URLSearchParams('composeId=9&severity=LOW&page=3'));
    expect(cveQueryFromSearch(cveQueryToSearch(q))).toEqual(q);
  });
});

describe('tagChoiceNeedsConfirm', () => {
  it('asks for young tags only', () => {
    const tag: ScannerTag = { tag: '0.76.1', digest: 'sha256:x', ref: 'r', pushedAt: '', ageDays: 6, young: true };
    expect(tagChoiceNeedsConfirm(tag)).toBe(true);
    expect(tagChoiceNeedsConfirm({ ...tag, young: false })).toBe(false);
  });
});

describe('parseDailyTime', () => {
  it.each([
    ['03:00', 180],
    ['23:59', 1439],
    ['24:00', null],
    ['3:00', null],
    ['', null],
  ])('%s', (value, minutes) => {
    expect(parseDailyTime(value)).toBe(minutes);
  });
});

describe('vulnLink', () => {
  it('opens the CVEs tab with the scope', () => {
    expect(vulnLink({})).toBe('/vulnerabilities?tab=cves');
    expect(vulnLink({ projectId: 4 })).toBe('/vulnerabilities?tab=cves&projectId=4');
    expect(vulnLink({ composeId: 7 })).toBe('/vulnerabilities?tab=cves&composeId=7');
  });
});
