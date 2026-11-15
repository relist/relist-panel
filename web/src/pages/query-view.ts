import type { QueryCell, QueryColumn, QueryResult } from '../data/types';

/** What a cell reads as in text and CSV: NULL is empty, a binary value is 0x… hex. */
export function cellText(cell: QueryCell): string {
  if (cell === null) return '';
  if (typeof cell === 'string') return cell;
  return cell.hex !== undefined ? `0x${cell.hex}` : (cell.text ?? '');
}

function csvField(text: string): string {
  return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

/** The shown rows as CSV (RFC 4180, CRLF), built in the browser. */
export function toCSV(columns: QueryColumn[], rows: QueryCell[][]): string {
  const lines = [columns.map((c) => csvField(c.name)).join(',')];
  for (const row of rows) lines.push(row.map((c) => csvField(cellText(c))).join(','));
  return `${lines.join('\r\n')}\r\n`;
}

export interface HistoryEntry {
  at: number;
  database: string;
  outcome: string;
  sql: string;
  writes: boolean;
}

const HISTORY_MAX = 50;
const historyKey = (id: number) => `relist.query.history.${id}`;

/** The last runs of a Database, newest first. Browser storage may be blocked: then there is none. */
export function readHistory(id: number): HistoryEntry[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(historyKey(id)) ?? '[]');
    return Array.isArray(parsed) ? (parsed as HistoryEntry[]).filter((e) => typeof e?.sql === 'string') : [];
  } catch {
    return [];
  }
}

export function pushHistory(id: number, entry: HistoryEntry): void {
  try {
    localStorage.setItem(historyKey(id), JSON.stringify([entry, ...readHistory(id)].slice(0, HISTORY_MAX)));
  } catch {
    // History is a convenience; the page works without it.
  }
}

/** The server counts characters; Monaco counts UTF-16 units. */
export function utf16Index(text: string, codePoints: number): number {
  let index = 0;
  let seen = 0;
  for (const ch of text) {
    if (seen >= codePoints) break;
    index += ch.length;
    seen++;
  }
  return index;
}

export function resultLabel(result: Pick<QueryResult, 'command'>, index: number): string {
  return `${index + 1} · ${result.command}`;
}
