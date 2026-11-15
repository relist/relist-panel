import { beforeEach, describe, expect, it, vi } from 'vitest';

import { cellText, pushHistory, readHistory, resultLabel, toCSV, utf16Index } from './query-view';

describe('cellText', () => {
  it('shows each form of a cell', () => {
    expect(cellText(null)).toBe('');
    expect(cellText('9007199254740993')).toBe('9007199254740993');
    expect(cellText({ bytes: 70000, text: 'abc' })).toBe('abc');
    expect(cellText({ bytes: 2, hex: 'fffe' })).toBe('0xfffe');
  });
});

describe('toCSV', () => {
  it('quotes quotes, commas and newlines, and leaves NULL empty', () => {
    const csv = toCSV([{ name: 'a', type: 'text' }, { name: 'b,"x"', type: 'text' }], [
      ['say "hi"', null],
      ['one,two', 'line\nbreak'],
      [{ bytes: 2, hex: 'fffe' }, ''],
    ]);
    expect(csv).toBe('a,"b,""x"""\r\n"say ""hi""",\r\n"one,two","line\nbreak"\r\n0xfffe,\r\n');
  });
});

function memoryStorage() {
  const data = new Map<string, string>();
  return {
    getItem: (k: string) => data.get(k) ?? null,
    removeItem: (k: string) => void data.delete(k),
    setItem: (k: string, v: string) => void data.set(k, v),
  };
}

describe('history', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
  });

  it('keeps the last 50 runs, newest first', () => {
    for (let i = 0; i < 55; i++) {
      pushHistory(1, { at: i, database: 'postgres', outcome: 'ok', sql: `SELECT ${i}`, writes: false });
    }
    const list = readHistory(1);
    expect(list).toHaveLength(50);
    expect(list[0].sql).toBe('SELECT 54');
    expect(list[49].sql).toBe('SELECT 5');
    expect(readHistory(2)).toEqual([]);
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
    expect(() => pushHistory(1, { at: 1, database: 'd', outcome: 'ok', sql: 'x', writes: false })).not.toThrow();
    expect(readHistory(1)).toEqual([]);
  });

  it('ignores a corrupt entry', () => {
    localStorage.setItem('relist.query.history.1', '{nope');
    expect(readHistory(1)).toEqual([]);
  });
});

describe('utf16Index', () => {
  it('turns a code point offset into a UTF-16 index', () => {
    expect(utf16Index('a😀b', 2)).toBe(3);
    expect(utf16Index('abc', 2)).toBe(2);
    expect(utf16Index('abc', 99)).toBe(3);
  });
});

describe('resultLabel', () => {
  it('numbers the result and names its command', () => {
    expect(resultLabel({ command: 'SELECT 100' } as never, 0)).toBe('1 · SELECT 100');
  });
});
