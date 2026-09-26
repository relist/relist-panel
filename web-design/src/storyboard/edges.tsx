/**
 * The storyboard's two edge renderers — `nav` (solid) and `opens` (dashed),
 * and only those two (spec: *a third would be decoration*).
 *
 * Both draw beziers with `vector-effect: non-scaling-stroke`, so a line is
 * 1.5px at every zoom level. `opens` carries a small closed arrowhead; note
 * the marker lives in the SVG's user space, so it scales with zoom even
 * though the stroke does not — acceptable, and the only thing about the
 * lines that could not be made zoom-invariant.
 */

import { BaseEdge, getBezierPath } from '@xyflow/react';
import type { Edge, EdgeProps, Position } from '@xyflow/react';

/** Default solid-line color (overridable per edge via `data.color`). */
export const DEFAULT_NAV_COLOR = '#64748b';
/** Default dashed-line color (overridable per edge via `data.color`). */
export const DEFAULT_OPENS_COLOR = '#d97706';
/** Stroke width of both kinds, in screen px (non-scaling). */
export const EDGE_STROKE_WIDTH = 1.5;
/** Dash pattern of `opens`, in user units (dashes do scale with zoom). */
export const OPENS_DASH_PATTERN = '7 5';

export type NavEdgeData = { color?: string; label?: string };
export type OpensEdgeData = { color?: string; label?: string };

export type NavFlowEdge = Edge<NavEdgeData, 'nav'>;
export type OpensFlowEdge = Edge<OpensEdgeData, 'opens'>;

const LABEL_BG = { fill: 'rgba(255, 255, 255, 0.85)' } as const;
const LABEL_PADDING: [number, number] = [6, 3];

type EdgePathProps = {
  sourceX: number;
  sourceY: number;
  sourcePosition: Position;
  targetX: number;
  targetY: number;
  targetPosition: Position;
  markerEnd?: string;
};

function edgePath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition }: EdgePathProps) {
  return getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
}

/** Solid navigation line: menu item → group header, tab bar → pane frame. */
export function NavEdge(props: EdgeProps<NavFlowEdge>) {
  const [path, labelX, labelY] = edgePath(props);
  return (
    <BaseEdge
      path={path}
      labelX={labelX}
      labelY={labelY}
      label={props.data?.label}
      labelShowBg
      labelBgStyle={LABEL_BG}
      labelBgPadding={LABEL_PADDING}
      labelBgBorderRadius={4}
      style={{
        stroke: props.data?.color ?? DEFAULT_NAV_COLOR,
        strokeWidth: EDGE_STROKE_WIDTH,
      }}
      vectorEffect="non-scaling-stroke"
      markerEnd={props.markerEnd}
      interactionWidth={8}
    />
  );
}

/** Dashed trigger line: an `opens` anchor on a frame → the overlay it opens. */
export function OpensEdge(props: EdgeProps<OpensFlowEdge>) {
  const [path, labelX, labelY] = edgePath(props);
  return (
    <BaseEdge
      path={path}
      labelX={labelX}
      labelY={labelY}
      label={props.data?.label}
      labelShowBg
      labelBgStyle={LABEL_BG}
      labelBgPadding={LABEL_PADDING}
      labelBgBorderRadius={4}
      style={{
        stroke: props.data?.color ?? DEFAULT_OPENS_COLOR,
        strokeWidth: EDGE_STROKE_WIDTH,
        strokeDasharray: OPENS_DASH_PATTERN,
      }}
      vectorEffect="non-scaling-stroke"
      markerEnd={props.markerEnd}
      interactionWidth={8}
    />
  );
}

/** Module-level so React Flow never sees a new edgeTypes object per render. */
export const edgeTypes = {
  nav: NavEdge,
  opens: OpensEdge,
};
