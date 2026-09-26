/**
 * The storyboard engine's pure layout rules, under `node --test` and nothing
 * else — the same stdlib pattern as `scripts/scene.test.mjs`.
 *
 * Only the render-free modules are imported: `layout.ts` (pure geometry, its
 * only import is a stripped `import type`) and `fixtures.ts` (data only). The
 * React half (`Board.tsx`, `nodes.tsx`, `edges.tsx`) is *not* importable from
 * here on purpose — it needs a DOM and is covered by ticket 06's browser
 * acceptance instead.
 *
 * Run: node --test scripts/storyboard.test.mjs
 */
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  FRAME_GAP,
  MENU_NODE_ID,
  MIN_COLUMN_WIDTH,
  RAIL_GAP,
  TAB_INDENT,
  anchorPoint,
  computeLayout,
  frameNodeId,
  groupHeaderId,
  tabAnchor,
} from '../src/storyboard/layout.ts';
import { fixtureGroups, fixtureMenu, fixtureScenes } from '../src/storyboard/fixtures.ts';

const layout = computeLayout(fixtureScenes, fixtureGroups, fixtureMenu);
const nodeById = new Map(layout.nodes.map((node) => [node.id, node]));
const frames = layout.nodes.filter((node) => node.kind === 'frame');
const headers = layout.nodes.filter((node) => node.kind === 'header');
const frame = (sceneId) => nodeById.get(frameNodeId(sceneId));

describe('computeLayout — columns', () => {
  it('orders the columns by group order and puts a header on each', () => {
    assert.deepEqual(headers.map((h) => h.id), fixtureGroups.map((g) => groupHeaderId(g.id)));
    for (let i = 1; i < headers.length; i++) {
      assert.ok(headers[i].x > headers[i - 1].x, 'columns must advance left to right in group order');
    }
  });

  it('places each group\'s top-level frames in its column, stacked in array order', () => {
    const core = headers.find((h) => h.groupId === 'core');
    const tops = ['projects', 'project-detail', 'compose'].map(frame);
    for (const top of tops) {
      assert.equal(top.x, core.x, 'top-level frames start at their column edge');
      assert.equal(top.columnId, 'core');
    }
    for (let i = 1; i < tops.length; i++) {
      assert.ok(tops[i].y >= tops[i - 1].y + tops[i - 1].height, 'frames stack top-down without overlap');
    }
    const ops = headers.find((h) => h.groupId === 'ops');
    assert.ok(frame('deployments').x === ops.x, 'other groups sit in their own column');
    assert.ok(ops.x > core.x + core.width, 'and strictly right of the wider column');
  });

  it('keeps tab-target frames in the parent\'s column at the tab indent, below the parent', () => {
    const compose = frame('compose');
    for (const paneId of ['compose-yaml', 'compose-logs']) {
      const pane = frame(paneId);
      assert.equal(pane.x, compose.x + TAB_INDENT, 'tab panes follow the parent in-column, indented');
      assert.ok(
        pane.y >= compose.y + compose.height + FRAME_GAP - 1e-6,
        'tab panes start below the parent frame',
      );
      assert.equal(pane.tab, true, 'tab panes are flagged so the renderer can draw the indent marker');
      assert.equal(pane.columnId, 'core');
    }
  });

  it('gives every column at least the minimum width, rails included', () => {
    for (const header of headers) {
      assert.ok(header.width >= MIN_COLUMN_WIDTH);
      const columnFrames = frames.filter((f) => f.columnId === header.groupId);
      for (const f of columnFrames) {
        assert.ok(f.x + f.width <= header.x + header.width + 1e-6, 'no frame exceeds its column width');
      }
    }
  });
});

describe('computeLayout — rails', () => {
  it('sits every overlay rail strictly right of its parent frame', () => {
    for (const scene of fixtureScenes) {
      if (!scene.parentId) continue;
      const child = frame(scene.id);
      const parent = frame(scene.parentId);
      assert.ok(child, 'rail child is placed');
      assert.ok(child.x >= parent.x + parent.width + RAIL_GAP - 1e-6, `${scene.id} sits right of ${scene.parentId}`);
      assert.equal(child.rail, true);
    }
  });

  it('stacks two scenes with the same parent without overlap', () => {
    const palette = frame('command-palette');
    const share = frame('share-dialog');
    assert.ok(share.y >= palette.y + palette.height, 'second overlay starts below the first');
  });

  it('pushes the column\'s next frame below a tall rail instead of overlapping it', () => {
    const palette = frame('command-palette');
    const detail = frame('project-detail');
    assert.ok(
      detail.y >= Math.max(palette.y + palette.height, frame('projects').y + frame('projects').height) + 1,
      'the rail bottom advances the column cursor',
    );
  });
});

describe('computeLayout — coordinates and sizes', () => {
  it('produces no negative coordinates, bounds included', () => {
    for (const node of layout.nodes) {
      assert.ok(node.x >= 0 && node.y >= 0, `${node.id} at ${node.x},${node.y} must be non-negative`);
    }
    assert.ok(layout.bounds.minX >= 0 && layout.bounds.minY >= 0);
    assert.ok(layout.bounds.maxX >= layout.bounds.minX && layout.bounds.maxY >= layout.bounds.minY);
  });

  it('lays every frame out at its declared natural size', () => {
    for (const scene of fixtureScenes) {
      const node = frame(scene.id);
      assert.ok(node, `${scene.id} is placed`);
      assert.equal(node.width, scene.size.width);
      assert.equal(node.height, scene.size.height);
    }
    assert.equal(frames.length, fixtureScenes.length, 'every fixture scene becomes exactly one frame');
  });
});

describe('computeLayout — edges', () => {
  it('references only node ids that exist, even with dangling declarations', () => {
    const ghost = computeLayout(
      [
        ...fixtureScenes,
        {
          id: 'ghost',
          title: 'Ghost',
          groupId: 'ops',
          kind: 'screen',
          size: { width: 400, height: 300 },
          opens: [{ anchor: [0.5, 0.5], targetId: 'does-not-exist' }],
        },
      ],
      fixtureGroups,
      [...fixtureMenu, { id: 'menu-ghost', label: 'Ghost', targetGroupId: 'no-such-group' }],
    );
    const ids = new Set(ghost.nodes.map((n) => n.id));
    for (const edge of ghost.edges) {
      assert.ok(ids.has(edge.source), `edge ${edge.id} source must exist`);
      assert.ok(ids.has(edge.target), `edge ${edge.id} target must exist`);
    }
    assert.ok(!ghost.edges.some((e) => e.id === 'sb-opens-ghost-0'), 'dangling opens draws no line to nowhere');
    assert.ok(!ghost.edges.some((e) => e.id === 'sb-nav-item-3'), 'menu item to a missing group draws no line');
  });

  it('draws one nav line per menu item, from the menu miniature to the group header', () => {
    const menu = nodeById.get(MENU_NODE_ID);
    assert.ok(menu && menu.kind === 'menu');
    assert.ok(menu.x + menu.width <= headers[0].x, 'menu sits left of the first column');
    for (let i = 0; i < fixtureMenu.length; i++) {
      const edge = layout.edges.find(
        (e) => e.kind === 'nav' && e.source === MENU_NODE_ID && e.target === groupHeaderId(fixtureMenu[i].targetGroupId),
      );
      assert.ok(edge, `menu item "${fixtureMenu[i].label}" has a nav line`);
      assert.equal(edge.sourceHandle, `item-${i}`);
      assert.equal(edge.label, fixtureMenu[i].label);
    }
  });

  it('draws opens lines from the parent frame to the overlay, dashed kind', () => {
    const edge = layout.edges.find((e) => e.kind === 'opens' && e.target === frameNodeId('command-palette'));
    assert.ok(edge, 'command-palette has an opens line');
    assert.equal(edge.source, frameNodeId('projects'));
    assert.equal(edge.sourceHandle, 'opens-0');
    assert.equal(edge.targetHandle, 'left', 'opens lines land on the overlay\'s left edge');
  });

  it('draws tab lines as solid nav, from the parent\'s top edge to the pane\'s top edge', () => {
    const edge = layout.edges.find((e) => e.kind === 'nav' && e.target === frameNodeId('compose-yaml'));
    assert.ok(edge, 'compose-yaml has a tab line');
    assert.equal(edge.source, frameNodeId('compose'));
    assert.equal(edge.sourceHandle, 'tab-0');
    assert.equal(edge.targetHandle, 'top');
  });
});

describe('anchor maths', () => {
  it('maps anchor fractions inside the parent box', () => {
    const projects = frame('projects');
    const point = anchorPoint(projects, [0.97, 0.05]);
    assert.ok(point.x >= projects.x && point.x <= projects.x + projects.width);
    assert.ok(point.y >= projects.y && point.y <= projects.y + projects.height);
  });

  it('clamps anchors outside [0, 1] into the box, never off it', () => {
    const box = { x: 100, y: 200, width: 400, height: 300 };
    assert.deepEqual(anchorPoint(box, [1.5, -0.4]), { x: 500, y: 200 });
    assert.deepEqual(anchorPoint(box, [-2, 3]), { x: 100, y: 500 });
    assert.deepEqual(anchorPoint(box, [0.5, 0.5]), { x: 300, y: 350 });
  });

  it('spreads tab anchors strictly inside the top edge, in tab order', () => {
    const first = tabAnchor(0, 2);
    const second = tabAnchor(1, 2);
    assert.ok(first > 0 && first < 1 && second > 0 && second < 1);
    assert.ok(first < second, 'tabs leave the top edge in declaration order');
    assert.equal(tabAnchor(1, 3), 0.5, 'the middle tab of three sits at the center');
  });
});
