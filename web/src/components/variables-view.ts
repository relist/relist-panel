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
 * The key-shape heuristic: the words no operator should publish in clear
 * text, with `key` counting only as a whole underscore-delimited word. The
 * migration that added the `secret` column backfilled existing rows by this
 * same pattern (as its SQL translation); since then the heuristic's only job
 * is suggesting the create-form default — never the tag or the masking.
 */
const SECRET_KEY_PATTERN = /secret|password|passwd|token|credential|(^|_)(api_?)?key($|_)|(^|_)private_?key/i;

export function isSecretKey(key: string): boolean {
  return SECRET_KEY_PATTERN.test(key);
}

/**
 * The stored flag, read alone. Every variable carries `secret` after the
 * backfill migration, so there is no guess to fall back on — the tag and the
 * masking answer whatever the user last saved (spec, variable-secrets).
 */
export function isSecret(variable: Pick<EnvironmentVariable, 'secret'>): boolean {
  return variable.secret;
}

/** The masked form of a value: dots, 8–16 long, whatever the value holds. */
export function maskedValue(value: string): string {
  return '•'.repeat(Math.min(16, Math.max(8, value.length)));
}

/** The panel's view of an Environment's variables: key-sorted, narrowed by the filter. */
export function visibleVariables<T extends { key: string }>(variables: T[], query: string): T[] {
  const needle = query.trim().toLowerCase();
  const sorted = [...variables].sort((a, b) => a.key.localeCompare(b.key));
  return needle ? sorted.filter((variable) => variable.key.toLowerCase().includes(needle)) : sorted;
}

export type ReplacePlan = {
  /** Keys the paste brings that the list does not have yet. */
  added: string[];
  /** Keys both hold with a different value; the paste overwrites them. */
  changed: string[];
  /** Keys both hold with the same value; nothing is written for them. */
  unchanged: string[];
  /** Keys the list holds that the paste drops — kept by a merge, removed by Replace all. */
  missing: string[];
};

/** What a pasted `.env` would do to a variable list, before it does it. */
export function replacePlan(current: readonly { key: string; value: string }[], rows: readonly DotEnvRow[]): ReplacePlan {
  const nextKeys = new Set(rows.map((row) => row.key));
  const plan: ReplacePlan = { added: [], changed: [], missing: [], unchanged: [] };
  for (const row of rows) {
    const existing = current.find((variable) => variable.key === row.key);
    if (!existing) plan.added.push(row.key);
    else if (existing.value === row.value) plan.unchanged.push(row.key);
    else plan.changed.push(row.key);
  }
  plan.missing = current.filter((variable) => !nextKeys.has(variable.key)).map((variable) => variable.key);
  return plan;
}

/**
 * The secret flag each pasted row is written with. A `.env` carries no flag, so
 * a key the list already has keeps its stored one (a re-paste must not unmask
 * it), and a new key takes the create form's guess from its name.
 */
export function pasteSecretFlags(
  current: readonly { key: string; secret: boolean }[],
  rows: readonly DotEnvRow[],
): { key: string; value: string; secret: boolean }[] {
  return rows.map((row) => ({
    ...row,
    secret: current.find((variable) => variable.key === row.key)?.secret ?? isSecretKey(row.key),
  }));
}

/**
 * The Environment Variable a Compose Variable overrides, if any: the one with
 * the same key. Matching is exact — keys are case-sensitive in `.env`.
 */
export function overriddenVariable<T extends { key: string }>(key: string, environmentVariables: readonly T[]): T | undefined {
  return environmentVariables.find((variable) => variable.key === key);
}

/**
 * The Compose Variables row's override mark, or `null` when nothing is
 * overridden: the tag's text, and the shadowed value for its tooltip. A
 * secret's value stays masked.
 */
export function overrideHint(
  key: string,
  environmentName: string,
  environmentVariables: readonly Pick<EnvironmentVariable, 'key' | 'secret' | 'value'>[],
): { label: string; detail: string } | null {
  const overridden = overriddenVariable(key, environmentVariables);
  if (!overridden) {
    return null;
  }
  return {
    detail: `${environmentName}: ${overridden.secret ? '••••' : overridden.value}`,
    label: `overrides ${environmentName}`,
  };
}

/** How many distinct keys a deploy writes: the Environment's, plus the Compose-only ones. */
export function effectiveVariableCount(
  environmentVariables: readonly { key: string }[],
  composeVariables: readonly { key: string }[],
): number {
  return new Set([...environmentVariables, ...composeVariables].map((variable) => variable.key)).size;
}
