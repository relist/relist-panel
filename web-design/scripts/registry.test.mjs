/**
 * The scene registry's invariants (ticket 04) under `node --test` — stdlib
 * runner, no new dependency, same pattern as `scene.test.mjs`.
 *
 * The registry is rebuilt here the way `src/scenes/registry.ts` builds it:
 * `groups.ts` owns the order, each `groups/<id>.ts` exports its scenes as a
 * function of the router's real `paths`, and this file flat-maps in group
 * order. `registry.ts` itself is *not* imported — its extensionless runtime
 * imports do not resolve under plain Node — so the assembly rule is pinned by
 * rebuilding it, and `GROUPS` stays the single source of the order.
 *
 * **Env-gated assertions.** "Every `ui`/`after` name is declared by some
 * screen" is asserted against `app/uiKeys.ts` and the `registerSceneAdapter`
 * calls across `src/` — but ticket 02's batches land in parallel, so those
 * names appear over time. The check is skipped unless
 * `REGISTRY_ASSERT_KEYS=1`; run it with the flag to see exactly which names
 * are still unbacked:
 *
 *   REGISTRY_ASSERT_KEYS=1 node --test scripts/registry.test.mjs
 *
 * Run: node --test scripts/registry.test.mjs
 */

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { describe, it } from 'node:test';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { parse, paths } from '../src/app/router.ts';
import { DECLARED_UI_KEYS } from '../src/app/uiKeys.ts';
import { GROUPS } from '../src/scenes/groups.ts';
import { orphanReport } from '../src/scenes/orphans.ts';
import { VIEWPORT_SIZES } from '../src/scenes/types.ts';

const HERE = dirname(fileURLToPath(import.meta.url));
const SRC = join(HERE, '../src');
const KEYS_GATED = process.env.REGISTRY_ASSERT_KEYS === '1';

/** The route kind a group's scenes claim — no scene may land in `not-found` by accident. */
const GROUP_ROUTE_KIND = {
  overview: 'overview',
  projects: 'projects',
  monitoring: 'monitoring',
  project: 'project',
  compose: 'compose',
  'volume-explorer': 'volumeExplorer',
  'design-system': 'guidelines',
  feedback: 'states',
};

/** Scenes whose claimed kind differs from their group's default. */
const ROUTE_KIND_OVERRIDES = {
  'auth-signin': 'signin',
  'auth-signin-submitting': 'signin',
  'auth-signin-errors': 'signin',
  'auth-setup': 'setup',
  'auth-setup-errors': 'setup',
  'auth-setup-ready': 'setup',
  'not-found': 'not-found',
};

/** camelCase export name of a group module, derived from the group id. */
const exportName = (id) => id.replace(/-([a-z])/g, (_, character) => character.toUpperCase());

// Rebuild the registry exactly as registry.ts does: GROUPS owns the order.
const groupModules = await Promise.all(
  GROUPS.map(async (group) => ({
    group,
    scenes: await import(`../src/scenes/groups/${group.id}.ts`).then(
      (module) => module[exportName(group.id)].scenes,
    ),
  })),
);
const SCENES = groupModules.flatMap(({ group, scenes }) => scenes({ ...paths }));

const byId = new Map(SCENES.map((scene) => [scene.id, scene]));
const ids = SCENES.map((scene) => scene.id);

/**
 * Declared `ui` key names and families (`DECLARED_UI_KEYS`; families end in
 * `.*` per ticket 02, D2), and the `after:` names registered anywhere under
 * `src/` — batches register adapters from their own screen files, so the scan
 * is repo-wide, not just `sceneAdapters.ts`.
 */

function collectRegisteredAdapters() {
  const names = new Set();
  const pattern = /registerSceneAdapter\(\s*['"`]([^'"`]+)['"`]/g;
  const walk = (directory) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) {
        walk(path);
      } else if (/\.(tsx?|mjs)$/.test(entry.name)) {
        const source = readFileSync(path, 'utf8');
        for (const match of source.matchAll(pattern)) {
          names.add(match[1]);
        }
      }
    }
  };
  walk(SRC);
  return names;
}

/** A scene's ui key is declared when named exactly or covered by a `family.*` entry. */
function matchingDeclaredKey(key, declared) {
  if (declared.includes(key)) return true;
  return declared.some((entry) => entry.endsWith('.*') && key.startsWith(entry.slice(0, -1)));
}

function collectKeyViolations() {
  const declared = DECLARED_UI_KEYS;
  const adapters = collectRegisteredAdapters();
  const violations = [];
  for (const scene of SCENES) {
    for (const key of Object.keys(scene.ui ?? {})) {
      if (!matchingDeclaredKey(key, declared)) {
        violations.push(`${scene.id}: ui key "${key}" is not declared in app/uiKeys.ts`);
      }
    }
    if (scene.after !== undefined && !adapters.has(scene.after)) {
      violations.push(`${scene.id}: after "${scene.after}" is not registered by any registerSceneAdapter call`);
    }
  }
  return violations;
}

describe('registry — ids', () => {
  it('has unique, kebab-case scene ids', () => {
    assert.equal(new Set(ids).size, ids.length, 'scene ids must be unique');
    for (const id of ids) {
      assert.match(id, /^[a-z0-9]+(-[a-z0-9]+)*$/, `id "${id}" must be kebab-case`);
    }
  });

  it('lands in the spec\'s 95–105 desktop-frame range', () => {
    assert.ok(SCENES.length >= 95 && SCENES.length <= 105, `scene count ${SCENES.length} outside 95–105`);
  });
});

describe('registry — groups', () => {
  it('declares the groups in menu order', () => {
    assert.deepEqual(
      GROUPS.map((group) => group.id),
      ['overview', 'projects', 'monitoring', 'project', 'compose', 'volume-explorer', 'design-system', 'auth', 'feedback'],
    );
  });

  it('keeps every scene inside its module\'s group, and never revisits a group', () => {
    const order = GROUPS.map((group) => group.id);
    const seen = [];
    for (const scene of SCENES) {
      assert.ok(order.includes(scene.group), `scene "${scene.id}" has an unknown group "${scene.group}"`);
      if (seen.at(-1) !== scene.group) {
        seen.push(scene.group);
      }
    }
    assert.deepEqual(
      seen,
      order.filter((groupId) => seen.includes(groupId)),
      'groups must appear in GROUPS order with no interleaving',
    );
  });
});

describe('registry — routes', () => {
  it('resolves every hash to the route kind the scene claims', () => {
    for (const scene of SCENES) {
      const route = parse(scene.hash);
      const expected = ROUTE_KIND_OVERRIDES[scene.id] ?? GROUP_ROUTE_KIND[scene.group];
      assert.equal(
        route.kind,
        expected,
        `scene "${scene.id}" hash "${scene.hash}" parses as ${route.kind}, claimed ${expected}`,
      );
    }
  });

  it('reaches not-found only where a scene claims it', () => {
    for (const scene of SCENES) {
      const kind = parse(scene.hash).kind;
      if (kind === 'not-found') {
        assert.equal(scene.id, 'not-found', `scene "${scene.id}" accidentally lands in not-found`);
      }
    }
  });

  it('builds the 404 from paths, not a route literal', () => {
    const scene = byId.get('not-found');
    assert.equal(scene.hash, `${paths.projects}/nope`);
  });

  it('has no two scenes with the same hash, forced state and transition', () => {
    const seen = new Map();
    for (const scene of SCENES) {
      const key = `${scene.hash}::${JSON.stringify(scene.ui ?? {})}::${scene.after ?? ''}`;
      assert.ok(!seen.has(key), `scenes "${seen.get(key)}" and "${scene.id}" are the same frame twice`);
      seen.set(key, scene.id);
    }
  });
});

describe('registry — forced state', () => {
  it('declares every ui/after name in uiKeys.ts or sceneAdapters.ts', { skip: !KEYS_GATED }, (t) => {
    const violations = collectKeyViolations();
    t.diagnostic(`checked ${SCENES.length} scenes against ${DECLARED_UI_KEYS.length} declared keys`);
    assert.deepEqual(violations, []);
  });

  it('forces state only through keys and adapters, never a "dark" variant', () => {
    for (const scene of SCENES) {
      const json = JSON.stringify(scene);
      assert.ok(!/"dark"\s*:/.test(json), `scene "${scene.id}" declares a dark field — the theme is a switch, not a mode`);
    }
  });
});

describe('registry — links', () => {
  it('references only real scene ids, from both ends', () => {
    for (const scene of SCENES) {
      for (const link of scene.links ?? []) {
        assert.ok(byId.has(link.to), `scene "${scene.id}" ${link.kind}-links to unknown scene "${link.to}"`);
        assert.ok(byId.has(link.from.scene), `scene "${scene.id}" ${link.kind}-links from unknown scene "${link.from.scene}"`);
        if (link.from.at !== undefined) {
          const [x, y] = link.from.at;
          assert.ok(x >= 0 && x <= 1 && y >= 0 && y <= 1, `scene "${scene.id}" anchor ${JSON.stringify(link.from.at)} is outside the frame`);
        }
      }
    }
  });

  it('carries an opens edge from its screen on every overlay, and the chain bottoms out at a screen', () => {
    for (const scene of SCENES) {
      if (scene.kind !== 'overlay') continue;
      const opens = (scene.links ?? []).filter((link) => link.kind === 'opens');
      assert.ok(opens.length > 0, `overlay "${scene.id}" has no opens link from its screen`);
      for (const link of opens) {
        const parent = byId.get(link.from.scene);
        assert.ok(parent, `overlay "${scene.id}" opens from unknown scene "${link.from.scene}"`);
        assert.ok(
          parent.kind === 'screen' || parent.kind === 'overlay',
          `overlay "${scene.id}" opens from "${link.from.scene}", which is neither a screen nor an overlay`,
        );
        assert.ok(link.from.at !== undefined, `overlay "${scene.id}" opens edge has no anchor fraction`);
        assert.equal(link.to, scene.id, `overlay "${scene.id}" opens edge must point at itself`);
      }
      // The chain of hosts must reach a screen without a cycle.
      const visited = new Set([scene.id]);
      let host = byId.get(opens[0].from.scene);
      while (host && host.kind === 'overlay') {
        assert.ok(!visited.has(host.id), `overlay "${scene.id}" opens chain has a cycle at "${host.id}"`);
        visited.add(host.id);
        const next = (host.links ?? []).find((link) => link.kind === 'opens');
        assert.ok(next, `overlay "${host.id}" in "${scene.id}"'s chain has no opens edge of its own`);
        host = byId.get(next.from.scene);
      }
      assert.equal(host?.kind, 'screen', `overlay "${scene.id}"'s opens chain never reaches a screen`);
    }
  });

  it('draws tab edges from screens to screens', () => {
    for (const scene of SCENES) {
      for (const link of (scene.links ?? []).filter((candidate) => candidate.kind === 'tab')) {
        assert.equal(scene.kind, 'screen', `scene "${scene.id}" declares a tab but is not a screen`);
        assert.equal(byId.get(link.to)?.kind, 'screen', `scene "${scene.id}" tab "${link.to}" is not a screen`);
        assert.ok(link.label, `scene "${scene.id}" tab to "${link.to}" has no label`);
      }
    }
  });

  it('leaves no overlay without a trigger (ticket 13\'s reachability invariant)', () => {
    // The menu-group set only feeds the report's third list (a group no menu
    // item names, like Compose, is reached by drilling); this assertion is
    // about links, so the set does not matter and is left empty on purpose —
    // the real one comes from the adapter on the page.
    const report = orphanReport(SCENES, new Set());
    assert.deepEqual(
      report.unlinkedOverlays.map((row) => row.sceneId),
      [],
      'an overlay nothing triggers is a state with no way to open it',
    );
  });
});

describe('registry — reviewability', () => {
  it('renders desktop only until ticket 03', () => {
    for (const scene of SCENES) {
      assert.deepEqual(scene.viewports, ['desktop'], `scene "${scene.id}" must be desktop-only until 03`);
    }
  });

  it('sizes every frame at its primary viewport with a positive height estimate', () => {
    for (const scene of SCENES) {
      assert.equal(scene.size.width, VIEWPORT_SIZES.desktop.width, `scene "${scene.id}" width is not the desktop viewport`);
      assert.ok(scene.size.height > 0, `scene "${scene.id}" has no height estimate`);
    }
  });

  it('writes captions that say more than the title does', () => {
    for (const scene of SCENES) {
      assert.ok(scene.caption.length > scene.title.length + 10, `scene "${scene.id}" caption restates its title`);
      assert.notEqual(scene.caption, scene.title);
    }
  });

  it('places every scene under a non-empty menu path', () => {
    for (const scene of SCENES) {
      assert.ok(Array.isArray(scene.menuPath) && scene.menuPath.length >= 1, `scene "${scene.id}" has no menuPath`);
      for (const segment of scene.menuPath) {
        assert.ok(typeof segment === 'string' && segment.length > 0, `scene "${scene.id}" has an empty menuPath segment`);
      }
    }
  });
});
