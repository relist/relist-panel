import type { ScannerTag, VulnCveQuery, VulnImage, VulnImageUser, VulnSeverity, VulnStatus, VulnSummary } from '../data/types';

export const SEVERITIES: VulnSeverity[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN'];

export const CVE_PAGE_SIZE = 20;

export const COPY = {
  off: 'Scanning is off',
  never: 'Not scanned yet',
  failed: (reason: string) => `Last scan failed: ${reason}`,
  youngTag: 'This tag is less than 14 days old. Use it anyway?',
};

export type SummaryBadge = { critical: number; high: number; fixable: number } | null;

/** The badge counts, or null to hide the badge: scanning off is not "0 CVEs". */
export function summaryBadge(summary: VulnSummary | undefined): SummaryBadge {
  if (!summary?.enabled || !summary.counts) {
    return null;
  }
  const { critical, high, fixable } = summary.counts;
  return { critical, high, fixable };
}

export interface ComposeNames {
  projectName: string;
  environmentName: string;
  composeName: string;
}

export function usedByLabel(user: VulnImageUser, names: Map<number, ComposeNames>): string {
  switch (user.kind) {
    case 'panel':
      return 'Panel';
    case 'traefik':
      return 'Traefik';
    case 'scanner':
      return 'Scanner (Trivy)';
  }
  const service = user.service ?? 'service';
  const compose = user.composeId === undefined ? undefined : names.get(user.composeId);
  return compose ? `${service} (${compose.projectName} / ${compose.environmentName} / ${compose.composeName})` : service;
}

export function dbFreshness(status: VulnStatus): 'ok' | 'stale' | 'missing' {
  if (!status.db.version) {
    return 'missing';
  }
  return status.db.stale ? 'stale' : 'ok';
}

export function scanRowState(image: VulnImage): 'ok' | 'never' | 'failed' | 'failedWithResults' {
  if (image.error) {
    return image.counts ? 'failedWithResults' : 'failed';
  }
  return image.counts ? 'ok' : 'never';
}

function positiveInt(value: string | null): number | undefined {
  if (value === null || !/^\d+$/.test(value)) {
    return undefined;
  }
  const n = Number(value);
  return n > 0 ? n : undefined;
}

export function cveQueryFromSearch(params: URLSearchParams): VulnCveQuery {
  const query: VulnCveQuery = {
    severity: (params.get('severity') ?? '')
      .split(',')
      .filter((s): s is VulnSeverity => (SEVERITIES as string[]).includes(s)),
    fixable: params.get('fixable') === '1',
    new: params.get('new') === '1',
    q: params.get('q') ?? '',
    page: positiveInt(params.get('page')) ?? 1,
    pageSize: positiveInt(params.get('pageSize')) ?? CVE_PAGE_SIZE,
  };
  const projectId = positiveInt(params.get('projectId'));
  const composeId = positiveInt(params.get('composeId'));
  if (projectId !== undefined) {
    query.projectId = projectId;
  }
  if (composeId !== undefined) {
    query.composeId = composeId;
  }
  return query;
}

/** The search string for a query, leaving defaults out. */
export function cveQueryToSearch(query: VulnCveQuery): URLSearchParams {
  const params = new URLSearchParams();
  if (query.projectId !== undefined) params.set('projectId', String(query.projectId));
  if (query.composeId !== undefined) params.set('composeId', String(query.composeId));
  if (query.severity.length > 0) params.set('severity', query.severity.join(','));
  if (query.fixable) params.set('fixable', '1');
  if (query.new) params.set('new', '1');
  if (query.q) params.set('q', query.q);
  if (query.page !== 1) params.set('page', String(query.page));
  if (query.pageSize !== CVE_PAGE_SIZE) params.set('pageSize', String(query.pageSize));
  return params;
}

export function tagChoiceNeedsConfirm(tag: ScannerTag): boolean {
  return tag.young;
}

/** `HH:MM` to minutes after midnight, null when it is not a time. */
export function parseDailyTime(value: string): number | null {
  const m = /^([01]\d|2[0-3]):([0-5]\d)$/.exec(value);
  return m ? Number(m[1]) * 60 + Number(m[2]) : null;
}
