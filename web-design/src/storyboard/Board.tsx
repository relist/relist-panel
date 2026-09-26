/**
 * The storyboard board: a Figma-style pan/zoom canvas of frames on
 * `@xyflow/react`. This component is the whole public component surface of the
 * engine; `computeLayout` (imported from `layout.ts`) is the whole public data
 * surface. It never imports antd, the router, the mock's data, or anything
 * else from `src/` outside `src/storyboard/` — the board page adapts the app's
 * scene registry to `BoardProps`.
 *
 * RAM mechanism (spec: *off-screen frames are unmounted*):
 * - `onlyRenderVisibleElements` — React Flow mounts only nodes fully inside
 *   the viewport (edges, only when both ends are visible); `FrameNode` reports
 *   mount/unmount so the app can show "mounted X/Y";
 * - the LOD floor in `nodes.tsx` keeps zoomed-out pans cheap;
 * - every node gets explicit `width`/`height`/`initialWidth`/`initialHeight`
 *   from the layout, so culling works before first measurement and remounted
 *   nodes never reflow anything.
 *
 * Pan/zoom feel: `panOnScroll` pans on wheel and zooms on ctrl/cmd+wheel and
 * pinch (React Flow's built-in Figma behavior — there is no separate
 * `zoomOnCtrl` prop); drag pans; keyboard zoom uses the default activation
 * key. `zoomOnScroll` and `zoomOnDoubleClick` are off, `minZoom` 0.08,
 * `maxZoom` 1.5, and `translateExtent` keeps the viewport near the board.
 */

import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef } from 'react';
import type { ReactNode, Ref } from 'react';
import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useNodesInitialized,
  useReactFlow,
} from '@xyflow/react';
import type { CoordinateExtent, Edge, Node, Viewport } from '@xyflow/react';
import '@xyflow/react/dist/style.css';

import type { BoardGroup, BoardMenuItem, BoardScene, LayoutNode } from './types';
import { computeLayout, groupColor } from './layout';
import { DEFAULT_OPENS_COLOR, edgeTypes } from './edges';
import { nodeTypes } from './nodes';
import type { FrameNodeData, HeaderNodeData } from './nodes';

// ---------------------------------------------------------------------------
// Public contract — pinned; the app's registry will be adapted to it.
// ---------------------------------------------------------------------------

export type BoardProps = {
  scenes: BoardScene[];
  groups: BoardGroup[];
  menu: BoardMenuItem[];
  /** Frame content factory; the engine knows nothing about what it renders. */
  renderFrame: (scene: BoardScene) => ReactNode;
  /** Below this zoom a frame renders as a name card, never its content. */
  lodBreak?: number;
  /** Fit/center on this frame at mount. */
  initialSceneId?: string;
  /** Mounted-frame census, as the visible proof of culling. */
  onMountStats?: (mounted: number, total: number) => void;
};

export type BoardHandle = {
  focusScene(id: string): void;
  fitGroup(groupId: string): void;
};

// ---------------------------------------------------------------------------
// Tuning
// ---------------------------------------------------------------------------

const DEFAULT_LOD_BREAK = 0.35;
/** focusScene never zooms below this. */
const FOCUS_ZOOM = 0.6;
const CAMERA_DURATION = 420;
const FIT_GROUP_PADDING = 0.15;
const FIT_VIEW_PADDING = 0.2;
/** How far outside the layout the viewport may wander. */
const PAN_MARGIN_X = 1600;
const PAN_MARGIN_Y = 1200;
const INITIAL_VIEWPORT: Viewport = { x: 0, y: 0, zoom: 0.3 };
const FIT_VIEW_OPTIONS = { padding: FIT_VIEW_PADDING, maxZoom: 1 };

type BoardFlowNode = Node<Record<string, unknown>>;

type InnerProps = BoardProps & { innerRef: Ref<BoardHandle> };

function BoardInner({ innerRef, ...props }: InnerProps) {
  const { scenes, groups, menu, renderFrame, lodBreak = DEFAULT_LOD_BREAK, initialSceneId, onMountStats } = props;

  const layout = useMemo(() => computeLayout(scenes, groups, menu), [scenes, groups, menu]);
  const { setCenter, fitView, getZoom } = useReactFlow();

  // ---- group colors (stable hash; MiniMap and name cards agree on them) ----

  const colors = useMemo(() => new Map(groups.map((g) => [g.id, groupColor(g.id)])), [groups]);
  const colorOf = useCallback((groupId: string) => colors.get(groupId) ?? groupColor(groupId), [colors]);

  // ---- mounted-frame census ------------------------------------------------

  const total = useMemo(() => layout.nodes.filter((n) => n.kind === 'frame').length, [layout]);
  const mountedRef = useRef(new Set<string>());
  const onMountStatsRef = useRef(onMountStats);
  useEffect(() => {
    onMountStatsRef.current = onMountStats;
  }, [onMountStats]);

  const onMountChange = useCallback(
    (sceneId: string, delta: 1 | -1) => {
      if (delta > 0) mountedRef.current.add(sceneId);
      else mountedRef.current.delete(sceneId);
      onMountStatsRef.current?.(mountedRef.current.size, total);
    },
    [total],
  );

  useEffect(() => {
    // Baseline census, so a readout exists before the first frame mounts.
    onMountStatsRef.current?.(mountedRef.current.size, total);
  }, [total]);

  // ---- React Flow nodes / edges (1:1 from the pure layout) -----------------

  const flowNodes = useMemo<BoardFlowNode[]>(
    () =>
      layout.nodes.map((box: LayoutNode) => {
        const common = {
          id: box.id,
          position: { x: box.x, y: box.y },
          // Explicit dimensions: culling works before measurement, and a
          // remounted node can never reflow the layout.
          width: box.width,
          height: box.height,
          initialWidth: box.width,
          initialHeight: box.height,
          style: { width: box.width, height: box.height },
        };
        switch (box.kind) {
          case 'frame':
            return {
              ...common,
              type: 'frame',
              data: {
                box,
                color: colorOf(box.scene?.groupId ?? ''),
                lodBreak,
                renderFrame,
                onMountChange,
              },
            };
          case 'header':
            return { ...common, type: 'header', data: { box, color: colorOf(box.groupId ?? '') } };
          case 'menu':
            return { ...common, type: 'menu', data: { box } };
        }
      }),
    [layout, colorOf, lodBreak, renderFrame, onMountChange],
  );

  const flowEdges = useMemo<Edge[]>(
    () =>
      layout.edges.map((edge) => ({
        id: edge.id,
        type: edge.kind,
        source: edge.source,
        sourceHandle: edge.sourceHandle,
        target: edge.target,
        targetHandle: edge.targetHandle,
        data: { label: edge.label },
        ...(edge.kind === 'opens'
          ? { markerEnd: { type: MarkerType.ArrowClosed, color: DEFAULT_OPENS_COLOR, width: 14, height: 14 } }
          : {}),
      })),
    [layout],
  );

  const translateExtent = useMemo<CoordinateExtent>(
    () => [
      [layout.bounds.minX - PAN_MARGIN_X, layout.bounds.minY - PAN_MARGIN_Y],
      [layout.bounds.maxX + PAN_MARGIN_X, layout.bounds.maxY + PAN_MARGIN_Y],
    ],
    [layout],
  );

  // ---- imperative handle ---------------------------------------------------

  const frameBoxes = useMemo(() => {
    const boxes = new Map<string, LayoutNode>();
    for (const node of layout.nodes) {
      if (node.kind === 'frame' && node.scene) boxes.set(node.scene.id, node);
    }
    return boxes;
  }, [layout]);

  const focusScene = useCallback(
    (id: string) => {
      const box = frameBoxes.get(id);
      if (!box) return;
      void setCenter(box.x + box.width / 2, box.y + box.height / 2, {
        zoom: Math.max(getZoom(), FOCUS_ZOOM),
        duration: CAMERA_DURATION,
      });
    },
    [frameBoxes, setCenter, getZoom],
  );

  const fitGroup = useCallback(
    (groupId: string) => {
      const ids = layout.nodes.filter((node) => node.columnId === groupId).map((node) => ({ id: node.id }));
      if (ids.length === 0) return;
      void fitView({ nodes: ids, padding: FIT_GROUP_PADDING, maxZoom: 1, duration: CAMERA_DURATION });
    },
    [layout, fitView],
  );

  useImperativeHandle(innerRef, () => ({ focusScene, fitGroup }), [focusScene, fitGroup]);

  // ---- mount behavior: center on the initial scene, else fit everything ----

  const nodesInitialized = useNodesInitialized();
  const didInitialFocus = useRef(false);
  useEffect(() => {
    if (!nodesInitialized || didInitialFocus.current) return;
    didInitialFocus.current = true;
    if (initialSceneId) focusScene(initialSceneId);
  }, [nodesInitialized, initialSceneId, focusScene]);

  // ---- render --------------------------------------------------------------

  const miniMapColor = useCallback((node: Node) => {
    const color = (node.data as Partial<FrameNodeData & HeaderNodeData>).color;
    return typeof color === 'string' ? color : '#dbe2ea';
  }, []);

  return (
    <div style={{ width: '100%', height: '100%' }}>
      <ReactFlow
        nodes={flowNodes}
        edges={flowEdges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        // The RAM mechanism: off-screen nodes must not be mounted.
        onlyRenderVisibleElements
        // The canvas is a board, not an editor.
        nodesDraggable={false}
        nodesConnectable={false}
        nodesFocusable={false}
        edgesFocusable={false}
        elementsSelectable={false}
        deleteKeyCode={null}
        // Figma feel: wheel pans, ctrl/cmd+wheel and pinch zoom (built into
        // React Flow's `panOnScroll` mode — there is no `zoomOnCtrl` prop).
        panOnScroll
        zoomOnPinch
        zoomOnScroll={false}
        panOnDrag
        zoomOnDoubleClick={false}
        minZoom={0.08}
        maxZoom={1.5}
        translateExtent={translateExtent}
        defaultViewport={INITIAL_VIEWPORT}
        fitView={!initialSceneId}
        fitViewOptions={FIT_VIEW_OPTIONS}
      >
        <Background variant={BackgroundVariant.Dots} gap={28} size={1.6} />
        <Controls position="bottom-left" showInteractive={false} />
        <MiniMap
          position="bottom-right"
          pannable
          zoomable
          bgColor="#f1f5f9"
          maskColor="rgba(241, 245, 249, 0.7)"
          nodeColor={miniMapColor}
          nodeStrokeWidth={0}
          nodeBorderRadius={4}
        />
      </ReactFlow>
    </div>
  );
}

/**
 * The board. Wrapped in its own `ReactFlowProvider` so `focusScene` /
 * `fitGroup` work through a plain ref — the app never has to know the provider
 * exists. Fill the parent: give the board's container explicit width/height
 * (the canvas measures itself against them).
 */
export const Board = forwardRef<BoardHandle, BoardProps>(function Board(props, ref) {
  return (
    <ReactFlowProvider>
      <BoardInner {...props} innerRef={ref} />
    </ReactFlowProvider>
  );
});
