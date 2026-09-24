import { describe, expect, it } from 'vitest';

import {
  duplicateKeys,
  invalidKeys,
  isSecret,
  isSecretKey,
  isValidKey,
  maskedValue,
  parseDotEnv,
  replacePlan,
  visibleVariables,
} from './variables-view';

describe('parseDotEnv', () => {
  it('reads one KEY=value per line and keeps values that contain equals signs', () => {
    expect(parseDotEnv('DATABASE_URL=postgres://app:pw@db:5432/app\nLOG_LEVEL=info')).toEqual([
      { key: 'DATABASE_URL', value: 'postgres://app:pw@db:5432/app' },
      { key: 'LOG_LEVEL', value: 'info' },
    ]);
  });

  it('skips blank lines and # comments', () => {
    expect(parseDotEnv('\n# a comment\n  # indented comment \nTZ=Asia/Bangkok\n')).toEqual([
      { key: 'TZ', value: 'Asia/Bangkok' },
    ]);
  });

  it('strips an export prefix and surrounding quotes', () => {
    expect(parseDotEnv('export KEY=one\nexport QUOTED="two words"\nSINGLE=\'three\'')).toEqual([
      { key: 'KEY', value: 'one' },
      { key: 'QUOTED', value: 'two words' },
      { key: 'SINGLE', value: 'three' },
    ]);
  });

  it('skips lines without a separator and lines with an empty key', () => {
    expect(parseDotEnv('NO_SEPARATOR\n=novalue\nOK=value')).toEqual([{ key: 'OK', value: 'value' }]);
  });

  it('keeps empty values and trims whitespace around keys and values', () => {
    expect(parseDotEnv('  SPACED   =   padded  \nEMPTY=')).toEqual([
      { key: 'SPACED', value: 'padded' },
      { key: 'EMPTY', value: '' },
    ]);
  });

  it('parses nothing from an empty paste', () => {
    expect(parseDotEnv('')).toEqual([]);
  });
});

describe('duplicateKeys', () => {
  it('reports each key defined twice or more, once, in first-duplicate order', () => {
    const rows = parseDotEnv('A=1\nB=2\nA=3\nC=4\nB=5\nA=6');
    expect(duplicateKeys(rows)).toEqual(['A', 'B']);
  });

  it('is empty for a clean paste', () => {
    expect(duplicateKeys(parseDotEnv('A=1\nB=2'))).toEqual([]);
  });
});

describe('invalidKeys', () => {
  it('reports keys no Compose file could reference, once each', () => {
    const rows = parseDotEnv('GOOD=1\nmy-var=2\n9STARTS=3\nWITH_DIGIT=4\n_a=5');
    expect(invalidKeys(rows)).toEqual(['my-var', '9STARTS']);
  });
});

describe('key validation', () => {
  it('accepts exactly the server-facing shape [A-Za-z_][A-Za-z0-9_]*', () => {
    expect(isValidKey('DATABASE_URL')).toBe(true);
    expect(isValidKey('_private')).toBe(true);
    expect(isValidKey('a')).toBe(true);
    expect(isValidKey('')).toBe(false);
    expect(isValidKey('9KEY')).toBe(false);
    expect(isValidKey('MY-VAR')).toBe(false);
    expect(isValidKey('MY VAR')).toBe(false);
    expect(isValidKey('trailing.dot')).toBe(false);
  });
});

describe('isSecret', () => {
  it('guesses from the key while the API carries no flag', () => {
    expect(isSecretKey('JWT_SECRET')).toBe(true);
    expect(isSecretKey('SMTP_PASSWORD')).toBe(true);
    expect(isSecretKey('API_KEY')).toBe(true);
    expect(isSecretKey('private_key_path')).toBe(true);
    expect(isSecretKey('LOG_LEVEL')).toBe(false);
    expect(isSecretKey('TZ')).toBe(false);
    expect(isSecretKey('MONKEY_PATCH')).toBe(false); // 'key' only counts on its own or at an underscore boundary
    expect(isSecretKey('HONEYPOT_KEY')).toBe(true);
    expect(isSecret({ key: 'JWT_SECRET' })).toBe(true);
    expect(isSecret({ key: 'TZ' })).toBe(false);
  });

  it('lets an explicit flag from the API win over the guess, both ways', () => {
    expect(isSecret({ key: 'JWT_SECRET', secret: false })).toBe(false);
    expect(isSecret({ key: 'TZ', secret: true })).toBe(true);
  });
});

describe('maskedValue', () => {
  it('is 8–16 dots, whatever the value holds', () => {
    expect(maskedValue('')).toBe('••••••••');
    expect(maskedValue('short')).toBe('••••••••');
    expect(maskedValue('exactly-12ch')).toHaveLength(12);
    expect(maskedValue('a-very-long-database-url-with-credentials')).toHaveLength(16);
  });
});

describe('visibleVariables', () => {
  const variables = [
    { id: 3, environmentId: 1, key: 'TZ', secret: undefined, updatedAt: '', value: 'x' },
    { id: 1, environmentId: 1, key: 'DATABASE_URL', secret: undefined, updatedAt: '', value: 'y' },
    { id: 2, environmentId: 1, key: 'JWT_SECRET', secret: undefined, updatedAt: '', value: 'z' },
  ] as Parameters<typeof visibleVariables>[0];

  it('sorts by key', () => {
    expect(visibleVariables(variables, '').map((v) => v.key)).toEqual(['DATABASE_URL', 'JWT_SECRET', 'TZ']);
  });

  it('narrows by the filter, case-insensitively, keeping the sort', () => {
    expect(visibleVariables(variables, 'jwt').map((v) => v.key)).toEqual(['JWT_SECRET']);
  });

  it('trims the query and yields everything for a blank one', () => {
    expect(visibleVariables(variables, '   ')).toHaveLength(3);
  });
});

describe('replacePlan', () => {
  it('splits the paste into added, updated and removed keys', () => {
    const current = [{ key: 'KEEP' }, { key: 'OVERWRITE' }, { key: 'DROPPED' }];
    const rows = parseDotEnv('OVERWRITE=new\nADDED=1\nKEEP=same');
    expect(replacePlan(current, rows)).toEqual({
      added: ['ADDED'],
      removed: ['DROPPED'],
      updated: ['OVERWRITE', 'KEEP'],
    });
  });

  it('replaces everything when the Environment is empty, and empties it when the paste is', () => {
    expect(replacePlan([], parseDotEnv('A=1'))).toEqual({ added: ['A'], removed: [], updated: [] });
    expect(replacePlan([{ key: 'A' }], [])).toEqual({ added: [], removed: ['A'], updated: [] });
  });
});
