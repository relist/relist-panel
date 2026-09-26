/**
 * The storyboard engine's data contract — types only, zero imports.
 *
 * This file is the bottom of the engine's import graph and must stay that way:
 * `layout.ts` and `fixtures.ts` are run under `node --test`, where a TypeScript
 * file resolves only when its runtime specifiers are bare (or absent). Every
 * import of this module anywhere in the engine is `import type`, which strips
 * to nothing — so this file never loads at runtime at all.
 *
 * The app-facing half of the contract (`BoardProps`, `BoardHandle`, which name
 * `ReactNode`) lives in `Board.tsx`; re-exported together through `index.ts`.
 * The board page (ticket 06) adapts the app's scene registry to these shapes.
 */

/** What a frame is showing: a screen, an overlay beside it, or a feedback note. */
export type BoardSceneKind = 'screen' | 'overlay' | 'feedback';

/** A trigger on the parent frame's box, drawn dashed to the overlay it opens. */
export type BoardOpensLink = {
  /**
   * Fraction of the PARENT frame's box, `[x, y]` in `[0, 1]`, where the dashed
   * line starts — how "the ⋯ button in the header" is expressible without
   * pixel-picking. Values outside `[0, 1]` are clamped, never dropped.
   */
  anchor: [number, number];
  targetId: string;
  label?: string;
};

/** An in-frame tab bar entry, drawn solid to the pane frame it shows. */
export type BoardTabLink = {
  label: string;
  targetId: string;
};

/** One frame on the board. */
export type BoardScene = {
  id: string;
  title: string;
  /** Which column the scene belongs to. */
  groupId: string;
  kind: BoardSceneKind;
  /** Natural (untransformed) size of the frame; the layout never reflows it. */
  size: { width: number; height: number };
  caption?: string;
  /** Overlay rail: the frame sits to the right of this parent frame. */
  parentId?: string;
  /** Triggers on this frame that open overlay frames (dashed lines). */
  opens?: BoardOpensLink[];
  /** In-frame tabs that show pane frames (solid lines from this frame's top edge). */
  tabs?: BoardTabLink[];
};

/** A column of the board, left to right in array order. */
export type BoardGroup = { id: string; label: string };

/** An entry of the inert menu miniature; each item draws one solid nav line. */
export type BoardMenuItem = { id: string; label: string; targetGroupId: string };

/** Structural kind of a laid-out node. */
export type LayoutNodeKind = 'menu' | 'header' | 'frame';

/**
 * One placed box on the canvas — pure data, no React. Produced by
 * `computeLayout`; `Board.tsx` turns these into React Flow nodes 1:1.
 */
export type LayoutNode = {
  id: string;
  kind: LayoutNodeKind;
  x: number;
  y: number;
  width: number;
  height: number;
  /** Set when kind is 'frame'. */
  scene?: BoardScene;
  /** Declared group id (frames) or the group a header opens (headers). */
  groupId?: string;
  /** The group whose column this box sits in (set on headers and frames). */
  columnId?: string;
  /** Display label (header title, menu caption). */
  label?: string;
  /** Set when kind is 'menu'; the items that source the nav lines. */
  menuItems?: BoardMenuItem[];
  /** Frame: sits in its parent's overlay rail (right of the parent). */
  rail?: boolean;
  /** Frame: a tab target following its parent in-column with an indent. */
  tab?: boolean;
};

/** The two line kinds — and only two (spec: a third would be decoration). */
export type LayoutEdgeKind = 'nav' | 'opens';

/** One declared connector — pure data; ids reference `LayoutNode.id`s. */
export type LayoutEdge = {
  id: string;
  kind: LayoutEdgeKind;
  source: string;
  sourceHandle: string;
  target: string;
  targetHandle: string;
  label?: string;
};

/** Canvas extent of everything `computeLayout` placed. */
export type LayoutBounds = { minX: number; minY: number; maxX: number; maxY: number };
