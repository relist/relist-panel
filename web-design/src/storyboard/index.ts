/**
 * Barrel for the storyboard engine — the one import surface the board page
 * (ticket 06) is meant to use:
 *
 * ```ts
 * import { Board, computeLayout, fixtures } from '…/src/storyboard';
 * ```
 *
 * The engine is standalone by construction: nothing here imports antd, the
 * router, the mock's data, or anything else from `src/` outside this folder.
 */

export type {
  BoardScene,
  BoardSceneKind,
  BoardOpensLink,
  BoardTabLink,
  BoardGroup,
  BoardMenuItem,
  LayoutNode,
  LayoutNodeKind,
  LayoutEdge,
  LayoutEdgeKind,
  LayoutBounds,
} from './types';

export type { BoardProps, BoardHandle } from './Board';
export { Board } from './Board';

export type { ComputeLayoutResult } from './layout';
export {
  computeLayout,
  anchorPoint,
  tabAnchor,
  groupColor,
  frameNodeId,
  groupHeaderId,
  MENU_NODE_ID,
  FRAME_TARGET_HANDLES,
  opensHandleId,
  tabHandleId,
  menuItemHandleId,
  HEADER_HEIGHT,
  HEADER_TO_FIRST_GAP,
  FRAME_GAP,
  COLUMN_GAP,
  RAIL_GAP,
  RAIL_STACK_GAP,
  TAB_INDENT,
  MENU_WIDTH,
  MENU_HEADER_HEIGHT,
  MENU_ROW_HEIGHT,
  MENU_GAP,
  MIN_COLUMN_WIDTH,
} from './layout';

export { nodeTypes, LOD_HYSTERESIS } from './nodes';
export type { FrameNodeData, HeaderNodeData, MenuNodeData, FrameFlowNode, HeaderFlowNode, MenuFlowNode } from './nodes';

export { edgeTypes, DEFAULT_NAV_COLOR, DEFAULT_OPENS_COLOR, EDGE_STROKE_WIDTH, OPENS_DASH_PATTERN } from './edges';
export type { NavEdgeData, OpensEdgeData, NavFlowEdge, OpensFlowEdge } from './edges';

export { fixtureScenes as fixtures, fixtureGroups, fixtureMenu } from './fixtures';
