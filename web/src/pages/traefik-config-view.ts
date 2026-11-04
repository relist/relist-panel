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
