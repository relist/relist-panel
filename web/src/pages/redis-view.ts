import type { RedisReply } from '../data/types';
import { formatBytes } from './databases-view';

function quote(text: string): string {
  return `"${text.replaceAll('\\', '\\\\').replaceAll('"', '\\"').replaceAll('\n', '\\n').replaceAll('\r', '\\r').replaceAll('\t', '\\t')}"`;
}

/** A reply as redis-cli prints it: (integer) 1, "text", (nil), numbered lines. */
export function formatReply(reply: RedisReply, indent = 0): string {
  switch (reply.type) {
    case 'integer':
      return `(integer) ${String(reply.value)}`;
    case 'string':
      return String(reply.value);
    case 'error':
      return `(error) ${String(reply.value)}`;
    case 'nil':
      return '(nil)';
    case 'bulk': {
      const text = quote(String(reply.value));
      return reply.bytes ? `${text} … cut, ${formatBytes(reply.bytes)}` : text;
    }
    case 'array': {
      const items = Array.isArray(reply.value) ? reply.value : [];
      if (items.length === 0 && !reply.more) return '(empty array)';
      const width = String(items.length).length;
      const pad = ' '.repeat(indent);
      const lines = items.map((item, i) => {
        const prefix = `${String(i + 1).padStart(width)}) `;
        // A nested array's later lines line up under its first.
        const body = formatReply(item, prefix.length).split('\n');
        return [`${i === 0 ? '' : pad}${prefix}${body[0]}`, ...body.slice(1).map((l) => l)].join('\n');
      });
      if (reply.more) lines.push(`${pad}… ${reply.more} more`);
      return lines.join('\n');
    }
  }
}

/** A key's TTL in words: -1 has none, -2 means the key is gone. */
export function ttlText(ms: number): string {
  if (ms === -1) return 'no expiry';
  if (ms < 0) return 'gone';
  const s = Math.round(ms / 1000);
  if (s < 60) return ms < 1000 ? `${ms} ms` : `${s} s`;
  const min = Math.floor(s / 60);
  if (min < 60) return s % 60 ? `${min} min ${s % 60} s` : `${min} min`;
  const h = Math.floor(min / 60);
  return min % 60 ? `${h} h ${min % 60} min` : `${h} h`;
}

const HISTORY_MAX = 50;
const key = (id: number) => `relist.redis.history.${id}`;

/** The last console lines of a Database, newest first; empty when browser storage is blocked. */
export function readRedisHistory(id: number): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(key(id)) ?? '[]');
    return Array.isArray(parsed) ? parsed.filter((l): l is string => typeof l === 'string') : [];
  } catch {
    return [];
  }
}

// A line that carries a password is not kept: browser storage is not a safe place for one.
const SECRET = /\b(auth2?|hello|requirepass|masterauth|setuser|migrate)\b/i;

export function pushRedisHistory(id: number, line: string): void {
  if (SECRET.test(line)) return;
  try {
    const list = readRedisHistory(id);
    if (list[0] === line) return;
    localStorage.setItem(key(id), JSON.stringify([line, ...list].slice(0, HISTORY_MAX)));
  } catch {
    // History is a convenience; the console works without it.
  }
}
