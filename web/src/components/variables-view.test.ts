import { describe, expect, it } from 'vitest';

import {
  duplicateKeys,
  invalidKeys,
  isSecret,
  isSecretKey,
  effectiveVariableCount,
  isValidKey,
  maskedValue,
  overrideHint,
  overriddenVariable,
  parseDotEnv,
  pasteSecretFlags,
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

describe('isSecretKey', () => {
  it('suggests the create-form default from the key shape, and nothing else', () => {
    expect(isSecretKey('JWT_SECRET')).toBe(true);
    expect(isSecretKey('SMTP_PASSWORD')).toBe(true);
    expect(isSecretKey('API_KEY')).toBe(true);
    expect(isSecretKey('private_key_path')).toBe(true);
    expect(isSecretKey('LOG_LEVEL')).toBe(false);
    expect(isSecretKey('TZ')).toBe(false);
    expect(isSecretKey('MONKEY_PATCH')).toBe(false); // 'key' only counts on its own or at an underscore boundary
    expect(isSecretKey('HONEYPOT_KEY')).toBe(true);
  });
});

describe('isSecret', () => {
  it('reads the stored flag alone — the guess has no seat at render time', () => {
    expect(isSecret({ secret: true })).toBe(true);
    expect(isSecret({ secret: false })).toBe(false);
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
    { id: 3, environmentId: 1, key: 'TZ', secret: false, updatedAt: '', value: 'x' },
    { id: 1, environmentId: 1, key: 'DATABASE_URL', secret: true, updatedAt: '', value: 'y' },
    { id: 2, environmentId: 1, key: 'JWT_SECRET', secret: true, updatedAt: '', value: 'z' },
  ];

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
  it('splits the paste into added, changed and unchanged keys, and lists the keys it drops', () => {
    const current = [
      { key: 'KEEP', value: 'same' },
      { key: 'OVERWRITE', value: 'old' },
      { key: 'DROPPED', value: 'x' },
    ];
    const rows = parseDotEnv('OVERWRITE=new\nADDED=1\nKEEP=same');
    expect(replacePlan(current, rows)).toEqual({
      added: ['ADDED'],
      changed: ['OVERWRITE'],
      missing: ['DROPPED'],
      unchanged: ['KEEP'],
    });
  });

  it('adds everything when the list is empty, and misses everything when the paste is', () => {
    expect(replacePlan([], parseDotEnv('A=1'))).toEqual({ added: ['A'], changed: [], missing: [], unchanged: [] });
    expect(replacePlan([{ key: 'A', value: '1' }], [])).toEqual({ added: [], changed: [], missing: ['A'], unchanged: [] });
  });
});

describe('pasteSecretFlags', () => {
  it('keeps the stored flag of a key the list already has, whatever its name says', () => {
    const current = [
      { key: 'DB_PASSWORD', secret: false },
      { key: 'SIGNING', secret: true },
    ];
    expect(pasteSecretFlags(current, parseDotEnv('DB_PASSWORD=x\nSIGNING=y'))).toEqual([
      { key: 'DB_PASSWORD', secret: false, value: 'x' },
      { key: 'SIGNING', secret: true, value: 'y' },
    ]);
  });

  it("guesses a new key's flag from its name", () => {
    expect(pasteSecretFlags([], parseDotEnv('API_TOKEN=t\nLOG_LEVEL=info'))).toEqual([
      { key: 'API_TOKEN', secret: true, value: 't' },
      { key: 'LOG_LEVEL', secret: false, value: 'info' },
    ]);
  });
});

describe('overrideHint', () => {
  const environmentVariables = [
    { key: 'LOG_LEVEL', secret: false, value: 'info' },
    { key: 'API_TOKEN', secret: true, value: 'hunter2' },
  ];

  it('names the Environment on the tag and shows a plain value in the detail', () => {
    expect(overrideHint('LOG_LEVEL', 'production', environmentVariables)).toEqual({
      detail: 'production: info',
      label: 'overrides production',
    });
  });

  it('masks a secret Environment Variable, whatever its length', () => {
    const hint = overrideHint('API_TOKEN', 'production', environmentVariables);
    expect(hint?.detail).toBe('production: ••••');
    expect(JSON.stringify(hint)).not.toContain('hunter2');
  });

  it('is null when no Environment Variable has the key, and follows it coming and going', () => {
    expect(overrideHint('DEBUG', 'production', environmentVariables)).toBeNull();
    expect(overrideHint('LOG_LEVEL', 'production', [])).toBeNull();
    expect(overrideHint('log_level', 'production', environmentVariables)).toBeNull();
  });
});

describe('overriddenVariable', () => {
  it('finds the Environment Variable with the same key', () => {
    expect(overriddenVariable('A', [{ key: 'B' }, { key: 'A' }])).toEqual({ key: 'A' });
    expect(overriddenVariable('C', [{ key: 'B' }])).toBeUndefined();
  });
});

describe('effectiveVariableCount', () => {
  it('counts an overridden key once and adds Compose-only keys', () => {
    expect(effectiveVariableCount([{ key: 'A' }, { key: 'B' }], [{ key: 'B' }, { key: 'C' }])).toBe(3);
    expect(effectiveVariableCount([], [])).toBe(0);
    expect(effectiveVariableCount([{ key: 'A' }], [])).toBe(1);
  });
});
