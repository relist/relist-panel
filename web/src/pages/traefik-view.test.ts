import { describe, expect, it } from 'vitest';

import { canSaveRetention, filterLogLines } from './traefik-view';

// Lines as `docker logs --timestamps` shows Traefik v3.7's own log (probe, 2026-11-03).
const ERR = '2026-11-03T07:32:27.041645720Z 2026-11-03T07:32:27Z ERR Unable to obtain ACME certificate domains=["a.test"]';
const WRN = '2026-11-03T07:32:28.000000000Z 2026-11-03T07:32:28Z WRN A new release has been found';
const INF = '2026-11-03T07:32:31.000000000Z 2026-11-03T07:32:31Z INF Closing and re-opening log files for rotation';
const RAW = '2026-11-03T07:32:32.000000000Z goroutine 1 [running]:';
const TEXT = [ERR, WRN, INF, RAW].join('\n') + '\n';

describe('filterLogLines', () => {
  it('keeps only errors at error', () => {
    expect(filterLogLines(TEXT, 'error')).toEqual([ERR]);
  });

  it('keeps errors and warnings at warn', () => {
    expect(filterLogLines(TEXT, 'warn')).toEqual([ERR, WRN]);
  });

  it('keeps errors, warnings and info at info', () => {
    expect(filterLogLines(TEXT, 'info')).toEqual([ERR, WRN, INF]);
  });

  it('keeps every line, untagged ones too, at all', () => {
    expect(filterLogLines(TEXT, 'all')).toEqual([ERR, WRN, INF, RAW]);
  });

  it('does not match a level word inside the message', () => {
    const line = '2026-11-03T07:32:31.0Z 2026-11-03T07:32:31Z INF router ERR-page loaded';
    expect(filterLogLines(line, 'error')).toEqual([]);
  });
});


describe('canSaveRetention', () => {
  const saved = { keepForever: false, retentionDays: 90 };

  it('needs a change', () => {
    expect(canSaveRetention(90, false, saved)).toBe(false);
    expect(canSaveRetention(30, false, saved)).toBe(true);
    expect(canSaveRetention(90, true, saved)).toBe(true);
  });

  it('refuses an empty or invalid number of days, even with keep forever', () => {
    expect(canSaveRetention(null, false, saved)).toBe(false);
    expect(canSaveRetention(null, true, saved)).toBe(false);
    expect(canSaveRetention(0, false, saved)).toBe(false);
  });
});
