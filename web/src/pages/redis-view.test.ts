import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { RedisReply } from '../data/types';
import { formatReply, pushRedisHistory, readRedisHistory, ttlText } from './redis-view';

describe('formatReply', () => {
  it('renders like redis-cli', () => {
    expect(formatReply({ type: 'integer', value: 1 })).toBe('(integer) 1');
    expect(formatReply({ type: 'string', value: 'OK' })).toBe('OK');
    expect(formatReply({ type: 'bulk', value: 'a "b"' })).toBe('"a \\"b\\""');
    expect(formatReply({ type: 'nil', value: null })).toBe('(nil)');
    expect(formatReply({ type: 'error', value: 'ERR nope' })).toBe('(error) ERR nope');
    expect(formatReply({ type: 'array', value: [] })).toBe('(empty array)');
  });

  it('numbers array lines, indents nested ones and counts the dropped', () => {
    const reply: RedisReply = {
      more: 500,
      type: 'array',
      value: [
        { type: 'bulk', value: 'a' },
        { type: 'array', value: [{ type: 'integer', value: 7 }, { type: 'nil', value: null }] },
      ],
    };
    expect(formatReply(reply)).toBe('1) "a"\n2) 1) (integer) 7\n   2) (nil)\n… 500 more');
  });

  it('marks a bulk string that was cut', () => {
    expect(formatReply({ bytes: 100000, type: 'bulk', value: 'xx' })).toBe('"xx" … cut, 97.7 KB');
  });
});

describe('ttlText', () => {
  it('says no expiry for -1', () => {
    expect(ttlText(-1)).toBe('no expiry');
    expect(ttlText(-2)).toBe('gone');
    expect(ttlText(5000)).toBe('5 s');
    expect(ttlText(90_000)).toBe('1 min 30 s');
  });
});

describe('redis history', () => {
  beforeEach(() => {
    const data = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => data.get(k) ?? null,
      setItem: (k: string, v: string) => void data.set(k, v),
    });
  });

  it('keeps the last 50, newest first, without repeating the previous one', () => {
    for (let i = 0; i < 55; i++) pushRedisHistory(1, `GET k${i}`);
    pushRedisHistory(1, 'GET k54');
    const list = readRedisHistory(1);
    expect(list).toHaveLength(50);
    expect(list[0]).toBe('GET k54');
    expect(list[1]).toBe('GET k53');
  });

  it('does not keep a line that carries a password', () => {
    pushRedisHistory(1, 'CONFIG SET requirepass hunter2');
    pushRedisHistory(1, 'GET k');
    expect(readRedisHistory(1)).toEqual(['GET k']);
  });

  it('survives a localStorage that throws', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('blocked');
      },
    });
    expect(() => pushRedisHistory(1, 'PING')).not.toThrow();
    expect(readRedisHistory(1)).toEqual([]);
  });
});
