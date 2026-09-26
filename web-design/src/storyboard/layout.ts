/**
 * The storyboard's pure layout math: scenes + groups + menu in, boxes and
 * connectors out. Render-free on purpose (spec: *the layout module is
 * render-free … it is what the flow layer draws lines against, and it is
 * testable without a browser*).
 *
 * No runtime imports at all — the `import type` below strips to nothing, which
 * is what lets `scripts/storyboard.test.mjs` run this file under `node --test`.
 *
 * Grammar (tickets 06/07):
 * - one column per group, left to right in group order, header on top;
 * - top-level frames stack top-down in scene-array order;
 * - a scene with `parentId` sits in an overlay rail to the RIGHT of its parent
 *   (parent's right edge + `RAIL_GAP`), children of one parent stacked;
 * - a tab target follows its parent in-column at `TAB_INDENT`, below the
 *   parent's own subtree;
 * - the menu miniature sits left of the first column; one solid nav edge per
 *   menu item to its target group's header;
 * - `opens` edges run from the parent frame's anchor fraction to the overlay's
 *   left edge; `tab` edges run solid from the parent's top edge to the pane's
 *   top edge.
 *
 * Dangling references never become edges (every edge references a placed
 * node), and anchors outside `[0, 1]` clamp into the parent box — a line to
 * nowhere is never drawn.
 */

import type {
  BoardGroup,
  BoardMenuItem,
  BoardScene,
  LayoutBounds,
  LayoutEdge,
  LayoutNode,
} from './types';

// ---------------------------------------------------------------------------
// Geometry constants — board units, i.e. css px of the untransformed canvas.
// ---------------------------------------------------------------------------

/** Group header bar height. */
export const HEADER_HEIGHT = 56;
/** Gap between a column header's bottom and the first frame under it. */
export const HEADER_TO_FIRST_GAP = 96;
/** Vertical gap between stacked frames in a column. */
export const FRAME_GAP = 112;
/** Horizontal gap between columns. */
export const COLUMN_GAP = 160;
/** Gap between a parent frame's right edge and its overlay rail. */
export const RAIL_GAP = 48;
/** Gap between two rail children of the same parent. */
export const RAIL_STACK_GAP = 48;
/** Left indent of a tab-target frame from its column edge. */
export const TAB_INDENT = 48;
/** Width of the inert menu miniature. */
export const MENU_WIDTH = 224;
/** Height of the menu miniature's caption bar. */
export const MENU_HEADER_HEIGHT = 44;
/** Height of one menu row; row i's nav line leaves at its vertical center. */
export const MENU_ROW_HEIGHT = 36;
/** Gap between the menu miniature and the first column. */
export const MENU_GAP = 120;
/** Floor for a column's width, so headers and empty groups stay visible. */
export const MIN_COLUMN_WIDTH = 320;

/** Node id of the menu miniature. */
export const MENU_NODE_ID = 'sb-menu';

/** Target-handle ids every frame renders. */
export const FRAME_TARGET_HANDLES = { left: 'left', top: 'top' } as const;

/** Source-handle id of the i-th `opens` trigger on a frame. */
export const opensHandleId = (i: number): string => `opens-${i}`;
/** Source-handle id of the i-th tab on a frame. */
export const tabHandleId = (i: number): string => `tab-${i}`;
/** Source-handle id of the i-th item on the menu miniature. */
export const menuItemHandleId = (i: number): string => `item-${i}`;

// ---------------------------------------------------------------------------
// Node id helpers
// ---------------------------------------------------------------------------

/** React-Flow node id for a scene's frame. */
export function frameNodeId(sceneId: string): string {
  return `sb-scene-${sceneId}`;
}

/** React-Flow node id for a group's header. */
export function groupHeaderId(groupId: string): string {
  return `sb-header-${groupId}`;
}

// ---------------------------------------------------------------------------
// Pure geometry helpers
// ---------------------------------------------------------------------------

/**
 * Maps an anchor fraction onto a box, clamping into `[0, 1]` — the maths the
 * flow layer's anchors are drawn with, shared by the layout and the renderer.
 */
export function anchorPoint(
  box: { x: number; y: number; width: number; height: number },
  anchor: readonly [number, number],
): { x: number; y: number } {
  const fx = Math.min(1, Math.max(0, anchor[0]));
  const fy = Math.min(1, Math.max(0, anchor[1]));
  return { x: box.x + box.width * fx, y: box.y + box.height * fy };
}

/**
 * Where the i-th of `count` tabs leaves a frame's top edge, as a fraction —
 * spread strictly inside `(0, 1)` so no line starts exactly on a corner.
 */
export function tabAnchor(index: number, count: number): number {
  if (count <= 0) return 0.5;
  return (index + 1) / (count + 1);
}

/**
 * Stable color for a group id: a tiny string hash onto a hue. No color map
 * needs to travel with the data, and the same group always reads as the same
 * color across the board, its minimap squares and its name cards.
 */
export function groupColor(groupId: string): string {
  let hash = 0;
  for (let i = 0; i < groupId.length; i++) {
    hash = (hash * 31 + groupId.charCodeAt(i)) | 0;
  }
  const hue = ((hash % 360) + 360) % 360;
  return `hsl(${hue} 58% 46%)`;
}

// ---------------------------------------------------------------------------
// computeLayout
// ---------------------------------------------------------------------------

export type ComputeLayoutResult = {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  bounds: LayoutBounds;
};

/**
 * Lays a scene registry out as boxes and declared connectors. Pure: same input,
 * same output, no React, no DOM. Dangling `parentId` / `opens` / `tabs` /
 * `targetGroupId` references are skipped rather than drawn to nowhere; scenes
 * whose `groupId` names no group are not placed.
 */
export function computeLayout(
  scenes: readonly BoardScene[],
  groups: readonly BoardGroup[],
  menu: readonly BoardMenuItem[],
): ComputeLayoutResult {
  const byId = new Map<string, BoardScene>();
  for (const scene of scenes) byId.set(scene.id, scene);
  const groupIds = new Set(groups.map((g) => g.id));

  // Who declares each tab target (first declaration wins), and what each
  // scene's placement role is. Precedence: an existing parent (rail) wins over
  // a tab declaration; a tab declaration wins over the scene's own group.
  const tabSourceOf = new Map<string, string>();
  for (const scene of scenes) {
    for (const tab of scene.tabs ?? []) {
      const target = byId.get(tab.targetId);
      if (!target || target.parentId) continue;
      if (!tabSourceOf.has(target.id)) tabSourceOf.set(target.id, scene.id);
    }
  }

  type Role = { kind: 'top' } | { kind: 'rail'; parentId: string } | { kind: 'tab'; sourceId: string };
  const roleOf = (scene: BoardScene): Role => {
    if (scene.parentId && byId.has(scene.parentId)) return { kind: 'rail', parentId: scene.parentId };
    const sourceId = tabSourceOf.get(scene.id);
    if (sourceId !== undefined) return { kind: 'tab', sourceId };
    return { kind: 'top' };
  };

  // Horizontal pre-pass: for every scene, which column it lands in and how far
  // right of that column's edge it sits (rails stack rightward, tab targets
  // indent). Memoised, with a cycle guard for malformed parent/tab chains.
  const offsetMemo = new Map<string, { columnId: string; dx: number } | null>();
  const inProgress = new Set<string>();
  const fallbackOffset = (scene: BoardScene): { columnId: string; dx: number } | null =>
    groupIds.has(scene.groupId) ? { columnId: scene.groupId, dx: 0 } : null;
  const offsetOf = (id: string): { columnId: string; dx: number } | null => {
    const scene = byId.get(id);
    if (!scene) return null;
    const memoed = offsetMemo.get(id);
    if (memoed !== undefined) return memoed;
    if (inProgress.has(id)) return fallbackOffset(scene);
    inProgress.add(id);
    const role = roleOf(scene);
    let result: { columnId: string; dx: number } | null;
    if (role.kind === 'rail') {
      const parent = byId.get(role.parentId)!;
      const parentOffset = offsetOf(role.parentId);
      result = parentOffset
        ? { columnId: parentOffset.columnId, dx: parentOffset.dx + parent.size.width + RAIL_GAP }
        : null;
    } else if (role.kind === 'tab') {
      const sourceOffset = offsetOf(role.sourceId);
      result = sourceOffset ? { columnId: sourceOffset.columnId, dx: sourceOffset.dx + TAB_INDENT } : null;
    } else {
      result = fallbackOffset(scene);
    }
    inProgress.delete(id);
    offsetMemo.set(id, result);
    return result;
  };

  // Column widths (widest occupant, rails and tab indents included) and x
  // positions, in group order; the menu occupies the left margin.
  const columnWidth = new Map<string, number>();
  for (const group of groups) {
    let width = MIN_COLUMN_WIDTH;
    for (const scene of scenes) {
      const offset = offsetOf(scene.id);
      if (offset && offset.columnId === group.id) {
        width = Math.max(width, offset.dx + scene.size.width);
      }
    }
    columnWidth.set(group.id, width);
  }
  const columnX = new Map<string, number>();
  let nextColumnX = MENU_WIDTH + MENU_GAP;
  for (const group of groups) {
    columnX.set(group.id, nextColumnX);
    nextColumnX += columnWidth.get(group.id)! + COLUMN_GAP;
  }

  // Vertical placement. `placeTree` drops a scene and everything hanging off
  // it (its rail, recursively, then its tab targets), returns the subtree's
  // bottom; the column cursor never revisits that band, so a tall rail pushes
  // the column's next frame down instead of overlapping it.
  const frameNodes: LayoutNode[] = [];
  const placed = new Map<string, LayoutNode>();
  const placing = new Set<string>();

  const placeTree = (
    scene: BoardScene,
    columnId: string,
    x: number,
    y: number,
    flags: { rail?: boolean; tab?: boolean },
  ): number => {
    if (placed.has(scene.id) || placing.has(scene.id)) return y;
    placing.add(scene.id);
    const node: LayoutNode = {
      id: frameNodeId(scene.id),
      kind: 'frame',
      x,
      y,
      width: scene.size.width,
      height: scene.size.height,
      scene,
      groupId: scene.groupId,
      columnId,
      ...(flags.rail ? { rail: true } : {}),
      ...(flags.tab ? { tab: true } : {}),
    };
    frameNodes.push(node);
    placed.set(scene.id, node);

    let bottom = y + scene.size.height;

    // Overlay rail: right of this frame, first child aligned under its top,
    // later children stacked below the previous child's subtree.
    let railY = y;
    for (const child of railChildrenOf(scene.id)) {
      const childBottom = placeTree(child, columnId, x + scene.size.width + RAIL_GAP, railY, { rail: true });
      railY = childBottom + RAIL_STACK_GAP;
      bottom = Math.max(bottom, childBottom);
    }

    // Tab targets: back in-column at the indent, below this frame's subtree.
    let tabY = bottom + FRAME_GAP;
    for (const pane of tabTargetsOf(scene.id)) {
      const paneBottom = placeTree(pane, columnId, columnX.get(columnId)! + TAB_INDENT, tabY, { tab: true });
      tabY = paneBottom + FRAME_GAP;
      bottom = Math.max(bottom, paneBottom);
    }

    placing.delete(scene.id);
    return bottom;
  };

  const railChildren = new Map<string, BoardScene[]>();
  const railChildrenOf = (parentId: string): BoardScene[] => {
    let list = railChildren.get(parentId);
    if (!list) {
      list = [];
      for (const scene of scenes) {
        if (roleOf(scene).kind === 'rail' && scene.parentId === parentId) list.push(scene);
      }
      railChildren.set(parentId, list);
    }
    return list;
  };

  const tabTargets = new Map<string, BoardScene[]>();
  const tabTargetsOf = (sourceId: string): BoardScene[] => {
    let list = tabTargets.get(sourceId);
    if (!list) {
      list = [];
      const source = byId.get(sourceId);
      for (const tab of source?.tabs ?? []) {
        const target = byId.get(tab.targetId);
        if (!target) continue;
        if (tabSourceOf.get(target.id) !== sourceId) continue; // rail child, or owned elsewhere
        if (!list.includes(target)) list.push(target);
      }
      tabTargets.set(sourceId, list);
    }
    return list;
  };

  const headerNodes: LayoutNode[] = groups.map((group) => ({
    id: groupHeaderId(group.id),
    kind: 'header' as const,
    x: columnX.get(group.id)!,
    y: 0,
    width: columnWidth.get(group.id)!,
    height: HEADER_HEIGHT,
    groupId: group.id,
    label: group.label,
    columnId: group.id,
  }));

  const cursorY = new Map<string, number>();
  for (const group of groups) cursorY.set(group.id, HEADER_HEIGHT + HEADER_TO_FIRST_GAP);
  for (const scene of scenes) {
    if (roleOf(scene).kind !== 'top') continue;
    if (!columnX.has(scene.groupId)) continue; // group not declared: not placed
    const y = cursorY.get(scene.groupId)!;
    const bottom = placeTree(scene, scene.groupId, columnX.get(scene.groupId)!, y, {});
    cursorY.set(scene.groupId, bottom + FRAME_GAP);
  }

  const menuNode: LayoutNode = {
    id: MENU_NODE_ID,
    kind: 'menu',
    x: 0,
    y: 0,
    width: MENU_WIDTH,
    height: MENU_HEADER_HEIGHT + menu.length * MENU_ROW_HEIGHT,
    label: 'Menu',
    menuItems: [...menu],
  };

  // Connectors — only between placed nodes, so an edge can never point at a
  // node id that does not exist.
  const edges: LayoutEdge[] = [];
  menu.forEach((item, i) => {
    if (!groupIds.has(item.targetGroupId)) return;
    edges.push({
      id: `sb-nav-item-${i}`,
      kind: 'nav',
      source: MENU_NODE_ID,
      sourceHandle: menuItemHandleId(i),
      target: groupHeaderId(item.targetGroupId),
      targetHandle: 'left',
      label: item.label,
    });
  });
  for (const scene of scenes) {
    (scene.opens ?? []).forEach((link, i) => {
      if (!placed.has(scene.id) || !placed.has(link.targetId)) return;
      edges.push({
        id: `sb-opens-${scene.id}-${i}`,
        kind: 'opens',
        source: frameNodeId(scene.id),
        sourceHandle: opensHandleId(i),
        target: frameNodeId(link.targetId),
        targetHandle: 'left',
        label: link.label,
      });
    });
    (scene.tabs ?? []).forEach((tab, i) => {
      if (!placed.has(scene.id) || !placed.has(tab.targetId)) return;
      edges.push({
        id: `sb-tab-${scene.id}-${i}`,
        kind: 'nav',
        source: frameNodeId(scene.id),
        sourceHandle: tabHandleId(i),
        target: frameNodeId(tab.targetId),
        targetHandle: 'top',
        label: tab.label,
      });
    });
  }

  const nodes: LayoutNode[] = [menuNode, ...headerNodes, ...frameNodes];
  let { minX, minY, maxX, maxY } = { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity };
  for (const node of nodes) {
    minX = Math.min(minX, node.x);
    minY = Math.min(minY, node.y);
    maxX = Math.max(maxX, node.x + node.width);
    maxY = Math.max(maxY, node.y + node.height);
  }
  const bounds: LayoutBounds = { minX, minY, maxX, maxY };

  return { nodes, edges, bounds };
}
