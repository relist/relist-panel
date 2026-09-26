/**
 * The storyboard's node renderers: the frame (with the LOD floor, mount
 * reporting and the engine-level error boundary), the group header and the
 * inert menu miniature.
 *
 * Engine-only: React and `@xyflow/react`, nothing else — no antd, no router,
 * no mock data. All styling is inline so the module carries no stylesheet of
 * its own beyond React Flow's.
 *
 * The LOD floor is the RAM mechanism's second half: below `lodBreak` zoom a
 * frame renders as a name card and `renderFrame` output is never mounted, so
 * zooming out never mounts every screen at once. Crossing the floor carries a
 * small hysteresis (`LOD_HYSTERESIS`) and the subscription only calls
 * `setState` when a threshold is actually crossed, so panning/zooming cannot
 * thrash content in and out at the boundary.
 */

import { Component, memo, useEffect, useState } from 'react';
import type { CSSProperties, ReactNode } from 'react';
import { Handle, Position, useStoreApi } from '@xyflow/react';
import type { Node, NodeProps } from '@xyflow/react';

import type { BoardScene, LayoutNode } from './types';
import {
  MENU_HEADER_HEIGHT,
  MENU_ROW_HEIGHT,
  anchorPoint,
  menuItemHandleId,
  opensHandleId,
  tabAnchor,
  tabHandleId,
} from './layout';

/** Extra zoom a collapsed frame needs before its content re-mounts. */
export const LOD_HYSTERESIS = 0.03;

// ---------------------------------------------------------------------------
// Node data + flow-node types (what Board.tsx puts in `node.data`)
// ---------------------------------------------------------------------------

export type FrameNodeData = {
  /** The laid-out box (scene, rail/tab flags). */
  box: LayoutNode;
  /** The frame group's stable color. */
  color: string;
  /** Below this zoom the frame renders as a name card. */
  lodBreak: number;
  /** Frame content factory — the engine knows nothing about what it renders. */
  renderFrame: (scene: BoardScene) => ReactNode;
  /** Mount/unmount report, aggregated by Board into `onMountStats`. */
  onMountChange: (sceneId: string, delta: 1 | -1) => void;
};

export type HeaderNodeData = { box: LayoutNode; color: string };
export type MenuNodeData = { box: LayoutNode };

export type FrameFlowNode = Node<FrameNodeData, 'frame'>;
export type HeaderFlowNode = Node<HeaderNodeData, 'header'>;
export type MenuFlowNode = Node<MenuNodeData, 'menu'>;

// ---------------------------------------------------------------------------
// Error boundary — a frame that throws is a red card naming its scene, never a
// blank board (spec: *per-frame isolation*).
// ---------------------------------------------------------------------------

class FrameErrorBoundary extends Component<
  { scene: BoardScene; children: ReactNode },
  { error: Error | null }
> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: unknown) {
    // Loud, per the flow-layer ticket's spirit: a broken frame names itself.
    console.error(`[storyboard] frame "${this.props.scene.id}" threw`, error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <div
          style={{
            width: '100%',
            height: '100%',
            boxSizing: 'border-box',
            display: 'grid',
            placeItems: 'center',
            padding: 24,
            border: '2px solid #dc2626',
            borderRadius: 10,
            background: '#fef2f2',
            color: '#b91c1c',
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            fontSize: 18,
            textAlign: 'center',
          }}
        >
          frame threw — {this.props.scene.id}
        </div>
      );
    }
    return this.props.children;
  }
}

/** Memoised so zoom-driven re-renders of FrameNode never re-render content. */
const FrameContent = memo(function FrameContent({
  scene,
  renderFrame,
}: {
  scene: BoardScene;
  renderFrame: (scene: BoardScene) => ReactNode;
}) {
  return <FrameErrorBoundary scene={scene}>{renderFrame(scene)}</FrameErrorBoundary>;
});

// ---------------------------------------------------------------------------
// Handles — every edge the grammar declares attaches to a real handle, so
// React Flow can compute endpoints (and cull edges) itself. They are invisible
// 1px dots with pointer events off; the node root is inert anyway.
// ---------------------------------------------------------------------------

/** Fractions-as-box, so `anchorPoint` yields clamped fractions. */
const UNIT_BOX = { x: 0, y: 0, width: 1, height: 1 };

const invisible: CSSProperties = {
  width: 1,
  height: 1,
  opacity: 0,
  pointerEvents: 'none',
  border: 'none',
};

/** Which side of the box an anchor handle reads as leaving from. */
function nearestEdge(fx: number, fy: number): Position {
  const candidates: [number, Position][] = [
    [fx, Position.Left],
    [1 - fx, Position.Right],
    [fy, Position.Top],
    [1 - fy, Position.Bottom],
  ];
  candidates.sort((a, b) => a[0] - b[0]);
  return candidates[0][1];
}

function anchorHandleStyle(fx: number, fy: number, position: Position): CSSProperties {
  const style: CSSProperties = {
    ...invisible,
    left: `${fx * 100}%`,
    top: `${fy * 100}%`,
    transform: 'translate(-50%, -50%)',
  };
  // The per-side classes set the opposite inset (right/bottom); drop it so the
  // inline left/top is the single source of the handle's position.
  if (position === Position.Right) style.right = 'auto';
  if (position === Position.Bottom) style.bottom = 'auto';
  return style;
}

// ---------------------------------------------------------------------------
// FrameNode
// ---------------------------------------------------------------------------

const NAME_CARD_TITLE: CSSProperties = {
  fontSize: 30,
  fontWeight: 600,
  color: '#0f172a',
  letterSpacing: '-0.01em',
  lineHeight: 1.2,
};

const NAME_CARD_META: CSSProperties = {
  marginTop: 10,
  fontSize: 18,
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  color: '#64748b',
};

function NameCard({ scene, color }: { scene: BoardScene; color: string }) {
  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        boxSizing: 'border-box',
        display: 'grid',
        placeItems: 'center',
        background: '#ffffff',
        border: `2px solid ${color}`,
        borderRadius: 12,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, maxWidth: '80%' }}>
        <span
          style={{
            flex: 'none',
            width: 18,
            height: 18,
            borderRadius: '50%',
            background: color,
          }}
        />
        <span>
          <div style={NAME_CARD_TITLE}>{scene.title}</div>
          <div style={NAME_CARD_META}>
            {scene.id} · {scene.size.width}×{scene.size.height}
          </div>
        </span>
      </div>
    </div>
  );
}

function FrameNodeComponent({ data }: NodeProps<FrameFlowNode>) {
  const { box, color, lodBreak, renderFrame, onMountChange } = data;
  const scene = box.scene as BoardScene;
  const store = useStoreApi();

  // Initial state from the current zoom: below the floor, renderFrame output is
  // never mounted at all — the LOD promise is kept from the first paint.
  const [detailed, setDetailed] = useState(() => store.getState().transform[2] >= lodBreak);

  // Hysteretic subscription: setState with an unchanged value is a no-op, so
  // this re-renders the node only when a threshold is actually crossed.
  useEffect(() => {
    const unsubscribe = store.subscribe((state) => {
      const zoom = state.transform[2];
      setDetailed((d) => (d ? zoom < lodBreak : zoom >= lodBreak + LOD_HYSTERESIS));
    });
    return unsubscribe;
  }, [store, lodBreak]);

  // The visible proof of culling: React Flow unmounts culled nodes under
  // `onlyRenderVisibleElements`, so mount/unmount bookkeeping lives here.
  useEffect(() => {
    onMountChange(scene.id, 1);
    return () => onMountChange(scene.id, -1);
  }, [onMountChange, scene.id]);

  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        boxSizing: 'border-box',
        overflow: 'hidden',
        background: '#ffffff',
        border: '1px solid rgba(15, 23, 42, 0.14)',
        borderRadius: 12,
        boxShadow: '0 10px 30px rgba(15, 23, 42, 0.12)',
        // Frames are static; only the board's own controls stay interactive.
        pointerEvents: 'none',
      }}
    >
      <div style={{ width: '100%', height: '100%', overflow: 'hidden' }}>
        {detailed ? (
          <FrameContent scene={scene} renderFrame={renderFrame} />
        ) : (
          <NameCard scene={scene} color={color} />
        )}
      </div>

      {/* Targets: opens lines land on the left edge, tab lines on the top edge. */}
      <Handle type="target" id="left" position={Position.Left} isConnectable={false} style={invisible} />
      <Handle type="target" id="top" position={Position.Top} isConnectable={false} style={invisible} />

      {/* Sources: one handle per declared `opens` anchor, at the anchor point. */}
      {(scene.opens ?? []).map((link, i) => {
        const point = anchorPoint(UNIT_BOX, link.anchor);
        const position = nearestEdge(point.x, point.y);
        return (
          <Handle
            key={opensHandleId(i)}
            type="source"
            id={opensHandleId(i)}
            position={position}
            isConnectable={false}
            style={anchorHandleStyle(point.x, point.y, position)}
          />
        );
      })}

      {/* Sources: tabs leave the top edge, spread by `tabAnchor`. */}
      {(scene.tabs ?? []).map((tab, i) => (
        <Handle
          key={tabHandleId(i)}
          type="source"
          id={tabHandleId(i)}
          position={Position.Top}
          isConnectable={false}
          style={{
            ...invisible,
            left: `${tabAnchor(i, scene.tabs?.length ?? 0) * 100}%`,
            top: 0,
            transform: 'translate(-50%, -50%)',
          }}
        />
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Group header + menu miniature
// ---------------------------------------------------------------------------

function GroupHeaderNode({ data }: NodeProps<HeaderFlowNode>) {
  const { box, color } = data;
  return (
    <div
      style={{
        width: '100%',
        height: '100%',
        boxSizing: 'border-box',
        display: 'flex',
        alignItems: 'flex-end',
        paddingBottom: 10,
        borderBottom: `3px solid ${color}`,
        pointerEvents: 'none',
      }}
    >
      <span style={{ fontSize: 22, fontWeight: 650, letterSpacing: '-0.01em', color: '#0f172a' }}>
        {box.label}
      </span>
      <Handle type="target" id="left" position={Position.Left} isConnectable={false} style={invisible} />
    </div>
  );
}

function MenuNode({ data }: NodeProps<MenuFlowNode>) {
  const { box } = data;
  const items = box.menuItems ?? [];
  return (
    <>
      <div
        style={{
          width: '100%',
          height: '100%',
          boxSizing: 'border-box',
          overflow: 'hidden',
          background: '#ffffff',
          border: '1px solid rgba(15, 23, 42, 0.16)',
          borderRadius: 10,
          boxShadow: '0 8px 24px rgba(15, 23, 42, 0.10)',
          pointerEvents: 'none',
        }}
      >
        <div
          style={{
            height: MENU_HEADER_HEIGHT,
            display: 'flex',
            alignItems: 'center',
            padding: '0 14px',
            fontWeight: 650,
            fontSize: 12,
            textTransform: 'uppercase',
            letterSpacing: '0.08em',
            color: '#475569',
            borderBottom: '1px solid rgba(15, 23, 42, 0.08)',
          }}
        >
          {box.label ?? 'Menu'}
        </div>
        {items.map((item) => (
          <div
            key={item.id}
            style={{
              height: MENU_ROW_HEIGHT,
              display: 'flex',
              alignItems: 'center',
              padding: '0 14px',
              fontSize: 14,
              color: '#334155',
              borderBottom: '1px solid rgba(15, 23, 42, 0.05)',
            }}
          >
            {item.label}
          </div>
        ))}
      </div>
      {items.map((item, i) => (
        <Handle
          key={item.id}
          type="source"
          id={menuItemHandleId(i)}
          position={Position.Right}
          isConnectable={false}
          style={{
            ...invisible,
            left: '100%',
            top: MENU_HEADER_HEIGHT + i * MENU_ROW_HEIGHT + MENU_ROW_HEIGHT / 2,
            transform: 'translate(0, -50%)',
          }}
        />
      ))}
    </>
  );
}

/** Module-level so React Flow never sees a new nodeTypes object per render. */
export const nodeTypes = {
  frame: FrameNodeComponent,
  header: GroupHeaderNode,
  menu: MenuNode,
};
