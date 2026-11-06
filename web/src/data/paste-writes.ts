/**
 * The per-row writes a pasted `.env` turns into. There is no bulk endpoint, so
 * every list (Environment, Compose, Variable Set) applies a paste through its
 * own create/update/delete calls; this decides which ones, against the list as
 * the caller read it.
 *
 * A row whose value and flag already match is skipped — writing it would stamp
 * `variablesChangedAt` for nothing. Keys the paste drops are deleted only when
 * the user asked to replace the whole list (`prune`); a plain paste merges.
 */
export type PasteRow = { key: string; value: string; secret: boolean };

export type PasteMode = { prune: boolean };

export type PasteWrites<Row extends PasteRow> = {
  creates: Row[];
  deletes: number[];
  updates: { id: number; row: Row }[];
};

export function pasteWrites<Row extends PasteRow>(
  current: readonly (PasteRow & { id: number })[],
  rows: readonly Row[],
  { prune }: PasteMode,
): PasteWrites<Row> {
  const writes: PasteWrites<Row> = { creates: [], deletes: [], updates: [] };
  if (prune) {
    for (const existing of current) {
      if (!rows.some((row) => row.key === existing.key)) writes.deletes.push(existing.id);
    }
  }
  for (const row of rows) {
    const existing = current.find((candidate) => candidate.key === row.key);
    if (!existing) writes.creates.push(row);
    else if (existing.value !== row.value || existing.secret !== row.secret) writes.updates.push({ id: existing.id, row });
  }
  return writes;
}
