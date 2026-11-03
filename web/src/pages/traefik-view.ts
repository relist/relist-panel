export type LogLevel = 'all' | 'error' | 'warn' | 'info';

/** The minimum rank each filter keeps. */
const MIN_RANK: Record<Exclude<LogLevel, 'all'>, number> = { error: 3, info: 1, warn: 2 };

const RANK: Record<string, number> = { DBG: 0, ERR: 3, FTL: 3, INF: 1, PNC: 3, TRC: 0, WRN: 2 };

/**
 * Traefik's level token, as `docker logs --timestamps` shows its log: Docker's
 * timestamp, Traefik's timestamp, then the level.
 */
const LEVEL = /^\S+ \S+ (TRC|DBG|INF|WRN|ERR|FTL|PNC) /;

/** The log's lines at or above level; `all` keeps untagged lines too. */
export function filterLogLines(text: string, level: LogLevel): string[] {
  const lines = text.split('\n').filter((line) => line !== '');
  if (level === 'all') return lines;
  return lines.filter((line) => {
    const token = LEVEL.exec(line)?.[1];
    return token !== undefined && RANK[token] >= MIN_RANK[level];
  });
}

/** Thai law asks service providers to keep traffic data for 90 days. */
export function showRetentionWarning(days: number, keepForever: boolean): boolean {
  return days < 90 && !keepForever;
}
