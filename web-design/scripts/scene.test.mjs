/**
 * The scene harness's pure rules, under `node --test` and nothing else.
 *
 * `web-design` has no test runner and this does not add one: `node:test` is
 * stdlib on Node 24. The two modules imported here are the harness's DOM-free
 * half — `uiState.ts` holds the parse and the coercion (and React, its only
 * import), `uiKeys.ts` holds the declared keys and the detection. `scene.ts` is
 * *not* importable from here on purpose: it is the browser bootstrap, and its
 * relative imports would not resolve outside Vite.
 *
 * Run: node --test web-design/scripts/scene.test.mjs
 */
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { coerceUiValue, parseSceneUrl } from '../src/app/uiState.ts';
import { createUndeclaredUiKeyWarner, DECLARED_UI_KEYS, STORE_UI_KEY_LIST, undeclaredUiKeys } from '../src/app/uiKeys.ts';

describe('parseSceneUrl', () => {
  it('reads scene, clock and after, and leaves the rest of the query string alone', () => {
    const scene = parseSceneUrl('?scene=compose-logs-fatal&clock=2026-09-26T09:00:00Z&after=submit&theme=dark');

    assert.equal(scene.scene, 'compose-logs-fatal');
    assert.equal(scene.clock, '2026-09-26T09:00:00Z');
    assert.equal(scene.after, 'submit');
    assert.deepEqual([...scene.ui], []);
  });

  it('reads repeated ui parameters into one map, in the order the URL carried them', () => {
    const scene = parseSceneUrl('?ui=compose.logsOpen=1&ui=compose.tab=logs');

    assert.deepEqual([...scene.ui], [
      ['compose.logsOpen', true],
      ['compose.tab', 'logs'],
    ]);
  });

  it('splits a ui entry on its first = only, so a value may contain one', () => {
    const scene = parseSceneUrl('?ui=volumeExplorer.editor=/app/uploads/a=b.txt');

    assert.equal(scene.ui.get('volumeExplorer.editor'), '/app/uploads/a=b.txt');
  });

  it('treats a bare ui key as on, and an empty value as the empty string', () => {
    assert.equal(parseSceneUrl('?ui=compose.logsOpen').ui.get('compose.logsOpen'), true);
    assert.equal(parseSceneUrl('?ui=compose.tab=').ui.get('compose.tab'), '');
  });

  it('decodes what the URL encoded, so a path with a space survives', () => {
    const scene = parseSceneUrl('?ui=volumeExplorer.editor=%2Fapp%2Fmy%20file.txt');

    assert.equal(scene.ui.get('volumeExplorer.editor'), '/app/my file.txt');
  });

  it('has nothing to say about an empty query string', () => {
    const scene = parseSceneUrl('');

    assert.deepEqual({ ...scene, ui: [...scene.ui] }, { after: null, clock: null, scene: null, ui: [] });
  });

  it('keeps a store override as a coerced value, since that is what sceneOverrides reads', () => {
    const scene = parseSceneUrl('?ui=store.speed=slow&ui=store.activity=empty&ui=store.handles=none');

    assert.equal(scene.ui.get('store.speed'), 'slow');
    assert.equal(scene.ui.get('store.activity'), 'empty');
    assert.equal(scene.ui.get('store.handles'), 'none');
  });
});

describe('coerceUiValue', () => {
  it('reads the four flag spellings as booleans', () => {
    assert.equal(coerceUiValue('1'), true);
    assert.equal(coerceUiValue('true'), true);
    assert.equal(coerceUiValue('0'), false);
    assert.equal(coerceUiValue('false'), false);
  });

  it('leaves everything else the string the URL carried', () => {
    assert.equal(coerceUiValue('logs'), 'logs');
    assert.equal(coerceUiValue('24h'), '24h');
    assert.equal(coerceUiValue(''), '');
    // A number is not inferred: 1 and 0 are flags, so 2 stays a string.
    assert.equal(coerceUiValue('2'), '2');
  });
});

describe('undeclared ui keys', () => {
  it('names the keys that are not declared, without repeats', () => {
    assert.deepEqual(undeclaredUiKeys(['known', 'typo', 'typo', 'other'], ['known']), ['typo', 'other']);
  });

  it('is quiet for every key the harness itself declares', () => {
    assert.deepEqual(undeclaredUiKeys(DECLARED_UI_KEYS), []);
    assert.deepEqual(undeclaredUiKeys(STORE_UI_KEY_LIST), []);
  });

  it('warns once per key and names the key, so a typo is loud rather than a silent no-op', () => {
    const messages = [];
    const warn = createUndeclaredUiKeyWarner((message) => messages.push(message), ['compose.tab']);

    warn(['compose.logsOpen', 'compose.logsOpen', 'compose.tab']);
    warn(['compose.logsOpen', 'compose.editor']);

    assert.equal(messages.length, 2);
    assert.match(messages[0], /compose\.logsOpen/);
    assert.match(messages[1], /compose\.editor/);
  });
});
