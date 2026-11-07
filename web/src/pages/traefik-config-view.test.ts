import { describe, expect, it } from 'vitest';

import { ipsFromText, isDirty, problemsByLine, readTimeoutWarning } from './traefik-config-view';

describe('isDirty', () => {
  it('compares the draft with the saved override', () => {
    expect(isDirty('a: 1\n', 'a: 1\n')).toBe(false);
    expect(isDirty('a: 2\n', 'a: 1\n')).toBe(true);
  });
});

describe('problemsByLine', () => {
  it('groups messages by line and skips line 0', () => {
    const map = problemsByLine([
      { key: 'a', line: 2, message: 'one' },
      { key: 'b', line: 2, message: 'two' },
      { key: '', line: 0, message: 'nowhere' },
    ]);
    expect([...map.entries()]).toEqual([[2, ['one', 'two']]]);
  });
});

describe('ipsFromText', () => {
  it('takes one IP or CIDR per line, trimmed, skipping blanks', () => {
    expect(ipsFromText(' 1.2.3.4 \n\n10.0.0.0/8\n')).toEqual(['1.2.3.4', '10.0.0.0/8']);
  });
});

describe('readTimeoutWarning', () => {
  it('warns below ten minutes', () => {
    expect(readTimeoutWarning('5m')).toBe(true);
    expect(readTimeoutWarning('90s')).toBe(true);
    expect(readTimeoutWarning('599')).toBe(true);
  });
  it('reads every Go duration spelling', () => {
    expect(readTimeoutWarning('+5m')).toBe(true);
    expect(readTimeoutWarning('300000000us')).toBe(true);
    expect(readTimeoutWarning('300000000µs')).toBe(true);
    expect(readTimeoutWarning('300000000000ns')).toBe(true);
    expect(readTimeoutWarning('+1h')).toBe(false);
  });
  it('stays quiet from ten minutes up and on text it cannot read', () => {
    expect(readTimeoutWarning('10m')).toBe(false);
    expect(readTimeoutWarning('3600')).toBe(false);
    expect(readTimeoutWarning('1h')).toBe(false);
    expect(readTimeoutWarning('1h30m')).toBe(false);
    expect(readTimeoutWarning('junk')).toBe(false);
    expect(readTimeoutWarning('')).toBe(false);
  });
});
