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
 * - frame *content* mounts only at rest (ticket 12): while the camera is
 *   moving every frame is its name card, so a pan never mounts an antd screen
 *   mid-gesture — the state lives here because a freshly culled mount must
 *   read "moving" too;
 * - every node gets explicit `width`/`height`/`initialWidth`/`initialHeight`
 *   from the layout, so culling works before first measurement and remounted
 *   nodes never reflow anything.
 *
 * Pan/zoom feel: `panOnScroll` pans on wheel and zooms on ctrl/cmd+wheel and
 * pinch (React Flow's built-in Figma behavior — there is no separate
 * `zoomOnCtrl` prop); drag pans; keyboard zoom uses the default activation
 * key. `zoomOnScroll` and `zoomOnDoubleClick` are off, `maxZoom` 1.5, and
 * `translateExtent` keeps the viewport near the board.
 *
 * `minZoom` is 0.02 because the board has to *fit*: 105 frames in nine columns
 * come to roughly 26 000 board units tall, so the mount-time `fitView` (and
 * the Fit control) needs ~0.024. At the previous 0.08 the fit was silently
 * clamped to the floor and the camera landed on a corner of the board — every
 * node still a name card at that zoom, so the lower floor costs nothing.
 */

import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
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
  useStoreApi,
} from '@xyflow/react';
import type { CoordinateExtent, Edge, Node, Viewport } from '@xyflow/react';
import '@xyflow/react/dist/style.css';

import type { BoardGroup, BoardMenuItem, BoardScene, LayoutNode } from './types';
import { computeLayout, groupColor } from './layout';
import { DEFAULT_OPENS_COLOR, edgeTypes } from './edges';
import { CAMERA_IDLE_MS, CameraIdleContext, nodeTypes } from './nodes';
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

  // ---- camera-idle gate (ticket 12) ----------------------------------------

  /**
   * Frame content is the expensive half of a node (a whole antd screen), so it
   * mounts only while the camera is at rest — see `CameraIdleContext`. The
   * subscription flips the state at exactly the two edges of a gesture: a
   * wheel tick with `moving` already false bails out of the update, so panning
   * never re-renders the board per event.
   */
  const store = useStoreApi();
  const [cameraIdle, setCameraIdle] = useState(true);
  useEffect(() => {
    let last = store.getState().transform;
    let timer: number | undefined;
    const unsubscribe = store.subscribe((state) => {
      if (state.transform === last) return;
      last = state.transform;
      setCameraIdle((idle) => (idle ? false : idle));
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        timer = undefined;
        setCameraIdle(true);
      }, CAMERA_IDLE_MS);
    });
    return () => {
      window.clearTimeout(timer);
      unsubscribe();
    };
  }, [store]);

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
      // setCenter resolves false while the pan/zoom surface is not mounted yet
      // (it mounts after the nodes do), so a mount-time focus must retry
      // instead of silently dropping — the ?scene= deep link has to land on
      // its frame every time, not on whichever paint won the race.
      void (async () => {
        for (let attempt = 0; attempt < 10; attempt += 1) {
          const applied = await setCenter(box.x + box.width / 2, box.y + box.height / 2, {
            zoom: Math.max(getZoom(), FOCUS_ZOOM),
            duration: attempt === 0 ? CAMERA_DURATION : 0,
          });
          if (applied !== false) return;
          await new Promise((resolve) => setTimeout(resolve, 120));
        }
      })();
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
      {/* The idle gate travels by context, so a node culled in mid-pan mounts
          as its name card rather than mounting content the gesture will hide
          again a hundred milliseconds later. */}
      <CameraIdleContext.Provider value={cameraIdle}>
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
          minZoom={0.02}
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
      </CameraIdleContext.Provider>
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
