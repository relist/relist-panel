import type { ConfigProblem } from '../data/types';

/** Whether the draft Traefik override differs from the saved one. */
export function isDirty(draft: string, saved: string): boolean {
  return draft !== saved;
}

/** Problem messages by override line; problems with no line are left out. */
export function problemsByLine(problems: ConfigProblem[]): Map<number, string[]> {
  const out = new Map<number, string[]>();
  for (const problem of problems) {
    if (problem.line <= 0) continue;
    out.set(problem.line, [...(out.get(problem.line) ?? []), problem.message]);
  }
  return out;
}

/** The Trusted proxies field: one IP or CIDR per line. */
export function ipsFromText(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line !== '');
}

const DURATION_PART = /(\d+(?:\.\d+)?)(ms|h|m|s)/g;
const UNIT_SECONDS: Record<string, number> = { h: 3600, m: 60, ms: 0.001, s: 1 };

/** Seconds in a Traefik duration ("90s", "5m", "1h30m", bare "3600"), or undefined. */
function durationSeconds(text: string): number | undefined {
  const value = text.trim();
  if (/^\d+$/.test(value)) return Number(value);
  if (!/^(\d+(?:\.\d+)?(ms|h|m|s))+$/.test(value)) return undefined;
  let total = 0;
  for (const [, amount, unit] of value.matchAll(DURATION_PART)) total += Number(amount) * UNIT_SECONDS[unit];
  return total;
}

/** Whether a read timeout is short enough to cut off large uploads and pushes (under ten minutes). */
export function readTimeoutWarning(text: string): boolean {
  const seconds = durationSeconds(text);
  return seconds !== undefined && seconds < 600;
}
