import type { EnvironmentVariable } from '../data/types';

/**
 * View decisions for Environment Variables, kept pure so the design's rules —
 * how a `.env` paste parses, when a bulk replace is blocked, what counts as a
 * key, when a value is masked — are testable without a component or a store.
 *
 * Lives beside the panel (not in `pages/`) because the full screen editor's
 * variables rail shares the masking and the key rule with it.
 */

/** The key shape a Compose file can reference as `${KEY}` (GUIDELINES §4.5). */
export const VARIABLE_KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function isValidKey(key: string): boolean {
  return VARIABLE_KEY_PATTERN.test(key);
}

export type DotEnvRow = {
  key: string;
  value: string;
};

/**
 * `.env` parsing for the bulk editor. Mirrors what `docker compose` accepts:
 * one `KEY=value` per line, `#` comments and blank lines skipped, an `export`
 * prefix stripped, surrounding quotes removed from the value.
 */
export function parseDotEnv(text: string): DotEnvRow[] {
  const rows: DotEnvRow[] = [];
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const withoutExport = line.startsWith('export ') ? line.slice(7).trim() : line;
    const separator = withoutExport.indexOf('=');
    if (separator <= 0) continue;
    const key = withoutExport.slice(0, separator).trim();
    let value = withoutExport.slice(separator + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    if (key) rows.push({ key, value });
  }
  return rows;
}

/** Keys the paste defines more than once, in first-duplicate order — a replace would make the winner arbitrary. */
export function duplicateKeys(rows: readonly DotEnvRow[]): string[] {
  const seen = new Set<string>();
  const duplicated = new Set<string>();
  for (const row of rows) {
    if (seen.has(row.key)) {
      duplicated.add(row.key);
    }
    seen.add(row.key);
  }
  return [...duplicated];
}

/** Keys in the paste that no Compose file could ever reference as `${KEY}`. */
export function invalidKeys(rows: readonly DotEnvRow[]): string[] {
  return [...new Set(rows.filter((row) => !isValidKey(row.key)).map((row) => row.key))];
}

/**
 * §11: the API carries no secret flag yet, so the marking is client-side for
 * now — a conservative read of the key's own shape (the words no operator
 * should publish in clear text). Once the API grows the flag it wins over the
 * guess: an explicit `false` keeps a value plain even if the key smells.
 */
const SECRET_KEY_PATTERN = /secret|password|passwd|token|credential|(^|_)(api_?)?key($|_)|(^|_)private_?key/i;

export function isSecretKey(key: string): boolean {
  return SECRET_KEY_PATTERN.test(key);
}

export function isSecret(variable: Pick<EnvironmentVariable, 'key' | 'secret'>): boolean {
  return variable.secret ?? isSecretKey(variable.key);
}

/** The masked form of a value: dots, 8–16 long, whatever the value holds. */
export function maskedValue(value: string): string {
  return '•'.repeat(Math.min(16, Math.max(8, value.length)));
}

/** The panel's view of an Environment's variables: key-sorted, narrowed by the filter. */
export function visibleVariables(variables: EnvironmentVariable[], query: string): EnvironmentVariable[] {
  const needle = query.trim().toLowerCase();
  const sorted = [...variables].sort((a, b) => a.key.localeCompare(b.key));
  return needle ? sorted.filter((variable) => variable.key.toLowerCase().includes(needle)) : sorted;
}

export type ReplacePlan = {
  /** Keys the paste brings that the Environment does not have yet. */
  added: string[];
  /** Keys the Environment holds that the paste drops — replacing removes them. */
  removed: string[];
  /** Keys both hold; replacing overwrites their values. */
  updated: string[];
};

/** What a bulk replace would do to the Environment's variables, before it does it. */
export function replacePlan(current: readonly { key: string }[], rows: readonly DotEnvRow[]): ReplacePlan {
  const nextKeys = new Set(rows.map((row) => row.key));
  const currentKeys = new Set(current.map((variable) => variable.key));
  return {
    added: rows.filter((row) => !currentKeys.has(row.key)).map((row) => row.key),
    removed: current.filter((variable) => !nextKeys.has(variable.key)).map((variable) => variable.key),
    updated: rows.filter((row) => currentKeys.has(row.key)).map((row) => row.key),
  };
}
