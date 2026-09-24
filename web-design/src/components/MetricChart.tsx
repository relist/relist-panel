import { Card, Flex, Typography, theme } from 'antd';
import dayjs from 'dayjs';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { PointerEvent as ReactPointerEvent } from 'react';
import { EmptyState } from './primitives';

const { Text } = Typography;

export type MetricUnit = 'bytes' | 'bytesPerSecond' | 'percent';
export type MetricPoint = { at: number; value: number };
export type MetricSeries = { key: string; label: string; points: MetricPoint[] };
export type MetricSummary = { avg: number; current: number; max: number; min: number };
export type MetricChartState = 'ok' | 'partial' | 'unavailable';
export type MetricAxis = 'fixed100' | 'auto';
export type MetricMarker = { at: number; label: string };

/**
 * The plot is a fixed-height box measured in CSS pixels: the component reads
 * its own width and draws in that unit, so hairlines stay hairlines and the
 * 10px axis labels are 10px at any column width. No viewBox scaling, which
 * would stretch the text with the chart.
 */
const PLOT_HEIGHT = 140;
const MARGIN = { bottom: 20, left: 46, right: 8, top: 12 } as const;
const INNER_HEIGHT = PLOT_HEIGHT - MARGIN.top - MARGIN.bottom;
const INNER_BOTTOM = MARGIN.top + INNER_HEIGHT;
const GRID_LEVELS = [0.25, 0.5, 0.75] as const;
const LABEL_LEVELS = [0, 0.25, 0.5, 0.75, 1] as const;

const BYTE_UNITS = ['B', 'kB', 'MB', 'GB', 'TB'] as const;

function formatBytes(value: number): string {
  let scaled = value;
  let unit = 0;
  while (scaled >= 1024 && unit < BYTE_UNITS.length - 1) {
    scaled /= 1024;
    unit += 1;
  }
  return `${scaled.toFixed(scaled >= 100 || unit === 0 ? 0 : 1)} ${BYTE_UNITS[unit]}`;
}

/** The reading the header shows, unit included. */
function formatMetricValue(value: number, unit: MetricUnit): string {
  if (unit === 'percent') return `${value.toFixed(1)}%`;
  if (unit === 'bytesPerSecond') return `${formatBytes(value)}/s`;
  return formatBytes(value);
}

/** Axis labels drop the unit — the header carries it, and the margin is 46px. */
function formatAxisValue(value: number, unit: MetricUnit): string {
  if (unit === 'percent') return `${Math.round(value)}%`;
  return formatBytes(value);
}

/** The largest 1 / 2 / 5 × 10ⁿ at or above `value`, so the axis ends on a number a human reads. */
function niceMax(value: number): number {
  if (value <= 0) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(value));
  const normalized = value / magnitude;
  const step = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
  return step * magnitude;
}

/**
 * `auto` takes the maximum from the data: a container can use more than one
 * core, so 150% is a reading, not an error, and a fixed 0–100 axis would clip
 * it. Percent keeps a floor — without one a quiet container renders an axis of
 * fractions of a percent.
 */
function axisMax(unit: MetricUnit, axis: MetricAxis, dataMax: number): number {
  if (unit !== 'percent') return niceMax(dataMax);
  return axis === 'auto' ? Math.max(5, niceMax(dataMax)) : 100;
}

/**
 * The design rule: never draw more points than the plot has pixels. Buckets are
 * half the pixel width because each bucket contributes both its minimum and its
 * maximum, so a spike that lives in one sample still reaches the screen instead
 * of being drawn between two columns and disappearing.
 */
function downsample(points: MetricPoint[], width: number): MetricPoint[] {
  if (points.length <= width) return points;
  const buckets = Math.max(1, Math.floor(width / 2));
  const bucketSize = points.length / buckets;
  const out: MetricPoint[] = [];

  for (let bucket = 0; bucket < buckets; bucket += 1) {
    const start = Math.floor(bucket * bucketSize);
    const end = Math.max(start + 1, Math.floor((bucket + 1) * bucketSize));
    let min = points[start];
    let max = points[start];
    for (let index = start + 1; index < end; index += 1) {
      const point = points[index];
      if (point.value < min.value) min = point;
      if (point.value > max.value) max = point;
    }
    // Keep time order, or the path zig-zags back over itself.
    if (min.at === max.at) out.push(min);
    else if (min.at < max.at) out.push(min, max);
    else out.push(max, min);
  }

  return out;
}

/** Tick spacing in minutes: 15 inside the hour, coarser only when labels would collide. */
function tickStepMinutes(spanMinutes: number, innerWidth: number): number {
  const steps = [5, 15, 30, 60, 120, 240, 360];
  const maxTicks = Math.max(2, Math.floor(innerWidth / 64));
  return steps.find((step) => spanMinutes / step <= maxTicks) ?? steps[steps.length - 1];
}

export function summarizeMetric(points: MetricPoint[]): MetricSummary {
  if (points.length === 0) return { avg: 0, current: 0, max: 0, min: 0 };
  let min = points[0].value;
  let max = points[0].value;
  let total = 0;
  for (const point of points) {
    if (point.value < min) min = point.value;
    if (point.value > max) max = point.value;
    total += point.value;
  }
  return { avg: total / points.length, current: points[points.length - 1].value, max, min };
}

function nearestPoint(points: MetricPoint[], at: number): MetricPoint | undefined {
  let nearest = points[0];
  for (const point of points) {
    if (Math.abs(point.at - at) < Math.abs(nearest.at - at)) nearest = point;
  }
  return nearest;
}

/**
 * A time series in a card. One chart, one window, no chart dependency: the plot
 * is inline SVG sized to its column, with the two states a monitoring screen
 * always grows — a window that is still filling, and a source that cannot be
 * read at all. `unavailable` exists so a container-scoped counter can never be
 * dressed up as host traffic.
 */
export function MetricChart({
  absolute,
  axis = 'fixed100',
  collecting,
  markers,
  note,
  series,
  state = 'ok',
  summary,
  title,
  unit,
  unavailableReason,
  warn,
}: {
  title: string;
  unit: MetricUnit;
  series: MetricSeries[];
  summary: MetricSummary;
  state?: MetricChartState;
  note?: string;
  /** `auto` ends the axis at a nice maximum above the data instead of at 100. */
  axis?: MetricAxis;
  /** Vertical references drawn over the series — a deploy, a restart, a config change. */
  markers?: MetricMarker[];
  /** The absolute reading behind a percent metric. `only` says avg/peak/low add nothing. */
  absolute?: { only?: boolean; total: number; used: number };
  /** How much of the window the panel has actually collected. */
  collecting?: { haveMinutes: number; ofMinutes: number };
  /** Renders the current value in `colorWarning`. The caller owns the threshold. */
  warn?: boolean;
  unavailableReason?: string;
}) {
  const { token } = theme.useToken();
  const plotRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(600);
  const [hoverAt, setHoverAt] = useState<number>();

  // The plot's width is read back from the DOM rather than assumed: the card is
  // as wide as its column, and a viewBox that kept an older width would draw the
  // line at the wrong scale. The layout effect reconciles on every render — the
  // ResizeObserver only reports the changes it sees.
  useLayoutEffect(() => {
    const node = plotRef.current;
    if (!node) return;
    const measured = Math.round(node.clientWidth);
    if (measured > 0) setWidth(measured);
  });

  useEffect(() => {
    const node = plotRef.current;
    if (!node) return;
    const observer = new ResizeObserver(() => {
      const measured = Math.round(node.clientWidth);
      if (measured > 0) setWidth(measured);
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  const seriesColors = useMemo(() => [token.colorPrimary, token.purple], [token.colorPrimary, token.purple]);

  const plotted = useMemo(
    () => series.map((entry) => ({ ...entry, points: downsample(entry.points, width) })),
    [series, width],
  );

  const bounds = useMemo(() => {
    let first = Number.POSITIVE_INFINITY;
    let last = Number.NEGATIVE_INFINITY;
    let max = 0;
    for (const entry of plotted) {
      for (const point of entry.points) {
        if (point.at < first) first = point.at;
        if (point.at > last) last = point.at;
        if (point.value > max) max = point.value;
      }
    }
    return { first, last, max };
  }, [plotted]);

  const pointCount = plotted.reduce((total, entry) => total + entry.points.length, 0);
  const innerWidth = Math.max(1, width - MARGIN.left - MARGIN.right);
  const span = Math.max(1, bounds.last - bounds.first);
  const hasData = Number.isFinite(bounds.first);
  const yMax = axisMax(unit, axis, hasData ? bounds.max : 0);

  const x = (at: number) => MARGIN.left + ((at - bounds.first) / span) * innerWidth;
  const y = (value: number) => INNER_BOTTOM - (Math.min(value, yMax) / yMax) * INNER_HEIGHT;

  // A marker outside the plotted range would draw in the margin, or outside the
  // card; the caller passes the events it knows about and the chart decides
  // which of them this window can show.
  const visibleMarkers = (markers ?? []).filter(
    (marker) => hasData && marker.at >= bounds.first && marker.at <= bounds.last,
  );

  const linePath = (points: MetricPoint[]) =>
    points.map((point, index) => `${index === 0 ? 'M' : 'L'}${x(point.at).toFixed(1)},${y(point.value).toFixed(1)}`).join(' ');

  const areaPath = (points: MetricPoint[]) =>
    `${linePath(points)} L${x(points[points.length - 1].at).toFixed(1)},${INNER_BOTTOM} L${x(points[0].at).toFixed(1)},${INNER_BOTTOM} Z`;

  const ticks = useMemo(() => {
    if (!Number.isFinite(bounds.first)) return [];
    const stepMs = tickStepMinutes(span / 60_000, innerWidth) * 60_000;
    const out: number[] = [];
    for (let at = Math.ceil(bounds.first / stepMs) * stepMs; at <= bounds.last; at += stepMs) out.push(at);
    return out;
  }, [bounds, innerWidth, span]);

  // `x` and `hasData` are both derived from `plotted`, which is a dependency.
  const hovered = useMemo(() => {
    if (hoverAt === undefined || !hasData) return undefined;
    const reference = nearestPoint(plotted[0].points, hoverAt);
    if (!reference) return undefined;
    return {
      at: reference.at,
      values: plotted.map((entry, index) => ({
        color: seriesColors[index % seriesColors.length],
        label: entry.label,
        value: nearestPoint(entry.points, reference.at)?.value ?? 0,
      })),
      x: x(reference.at),
    };
  }, [hoverAt, plotted, hasData, seriesColors]);

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!hasData) return;
    const rect = event.currentTarget.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left - MARGIN.left) / innerWidth));
    setHoverAt(bounds.first + ratio * span);
  };

  const currentValue =
    state === 'unavailable' ? (
      '—'
    ) : (
      <span
        style={{
          color: warn ? token.colorWarning : undefined,
          fontSize: 16,
          fontVariantNumeric: 'tabular-nums',
          fontWeight: 600,
        }}
      >
        {formatMetricValue(summary.current, unit)}
      </span>
    );

  if (state === 'unavailable') {
    return (
      <Card extra={currentValue} styles={{ body: { padding: 16 } }} title={title} variant="outlined">
        <EmptyState
          description={unavailableReason ?? 'The panel could not read this metric from the host.'}
          title="Source not readable"
        />
      </Card>
    );
  }

  const footer: string[] = [];
  if (!absolute?.only) {
    footer.push(`avg ${formatMetricValue(summary.avg, unit)}`);
    footer.push(`peak ${formatMetricValue(summary.max, unit)}`);
    footer.push(`low ${formatMetricValue(summary.min, unit)}`);
  }
  if (absolute) footer.push(`${formatBytes(absolute.used)} of ${formatBytes(absolute.total)} used`);

  return (
    <Card extra={currentValue} styles={{ body: { padding: 16 } }} title={title} variant="outlined">
      {state === 'partial' && collecting && (
        <Text style={{ color: token.colorWarning, display: 'block', fontSize: 12, marginBottom: 6 }}>
          collecting — {collecting.haveMinutes} of {collecting.ofMinutes} minutes
        </Text>
      )}

      {series.length > 1 && (
        <Flex gap={16} style={{ marginBottom: 6 }} wrap>
          {series.map((entry, index) => (
            <Flex align="center" gap={6} key={entry.key}>
              <span
                style={{
                  background: seriesColors[index % seriesColors.length],
                  borderRadius: 2,
                  height: 8,
                  width: 8,
                }}
              />
              <Text style={{ fontSize: 12 }} type="secondary">
                {entry.label}
              </Text>
              <Text style={{ fontSize: 12 }}>
                {formatMetricValue(entry.points[entry.points.length - 1]?.value ?? 0, unit)}
              </Text>
            </Flex>
          ))}
        </Flex>
      )}

      <div
        onPointerLeave={() => setHoverAt(undefined)}
        onPointerMove={onPointerMove}
        ref={plotRef}
        style={{ height: PLOT_HEIGHT, position: 'relative' }}
      >
        {/* `data-points` publishes the post-downsampling count, so the rule above is checkable
            rather than assumed. */}
        <svg
          aria-label={`${title} over the selected window`}
          data-markers={visibleMarkers.length}
          data-points={pointCount}
          height={PLOT_HEIGHT}
          role="img"
          style={{ display: 'block' }}
          viewBox={`0 0 ${width} ${PLOT_HEIGHT}`}
          width="100%"
        >
          {GRID_LEVELS.map((level) => (
            <line
              key={level}
              stroke={token.colorBorderSecondary}
              strokeWidth={1}
              vectorEffect="non-scaling-stroke"
              x1={MARGIN.left}
              x2={width - MARGIN.right}
              y1={INNER_BOTTOM - level * INNER_HEIGHT}
              y2={INNER_BOTTOM - level * INNER_HEIGHT}
            />
          ))}
          <line
            stroke={token.colorBorderSecondary}
            strokeWidth={1}
            vectorEffect="non-scaling-stroke"
            x1={MARGIN.left}
            x2={width - MARGIN.right}
            y1={INNER_BOTTOM}
            y2={INNER_BOTTOM}
          />

          {LABEL_LEVELS.map((level) => (
            <text
              dominantBaseline="middle"
              fill={token.colorTextTertiary}
              fontSize={10}
              key={level}
              textAnchor="end"
              x={MARGIN.left - 6}
              y={INNER_BOTTOM - level * INNER_HEIGHT}
            >
              {formatAxisValue(level * yMax, unit)}
            </text>
          ))}

          {hasData &&
            ticks.map((at) => (
              <text
                fill={token.colorTextTertiary}
                fontSize={10}
                key={at}
                textAnchor={x(at) < MARGIN.left + 16 ? 'start' : x(at) > width - MARGIN.right - 16 ? 'end' : 'middle'}
                x={x(at)}
                y={PLOT_HEIGHT - 5}
              >
                {dayjs(at).format('HH:mm')}
              </text>
            ))}

          {hasData &&
            plotted.map((entry, index) => {
              const color = seriesColors[index % seriesColors.length];
              return (
                <g key={entry.key}>
                  {series.length === 1 && <path d={areaPath(entry.points)} fill={color} fillOpacity={0.1} stroke="none" />}
                  <path d={linePath(entry.points)} fill="none" stroke={color} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
                </g>
              );
            })}

          {/* Over the series, not under it: a marker is a reference the lines are
              read against, so the line must not swallow it. */}
          {visibleMarkers.map((marker) => {
            const at = x(marker.at);
            return (
              <g key={`${marker.at}-${marker.label}`}>
                <line
                  stroke={token.colorTextTertiary}
                  strokeDasharray="4 3"
                  strokeWidth={1}
                  vectorEffect="non-scaling-stroke"
                  x1={at}
                  x2={at}
                  y1={MARGIN.top}
                  y2={INNER_BOTTOM}
                />
                <text
                  fill={token.colorTextTertiary}
                  fontSize={10}
                  textAnchor={at < MARGIN.left + 40 ? 'start' : at > width - MARGIN.right - 40 ? 'end' : 'middle'}
                  x={at}
                  y={MARGIN.top + 8}
                >
                  {marker.label}
                </text>
              </g>
            );
          })}

          {hovered && (
            <g>
              <line
                stroke={token.colorTextQuaternary}
                strokeWidth={1}
                vectorEffect="non-scaling-stroke"
                x1={hovered.x}
                x2={hovered.x}
                y1={MARGIN.top}
                y2={INNER_BOTTOM}
              />
              {hovered.values.map((entry) => (
                <circle cx={hovered.x} cy={y(entry.value)} fill={entry.color} key={entry.label} r={2.5} />
              ))}
            </g>
          )}
        </svg>

        {hovered && (
          <div
            data-metric-tooltip
            style={{
              background: token.colorBgElevated,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: token.borderRadius,
              boxShadow: token.boxShadowSecondary,
              fontSize: 12,
              left: hovered.x > width / 2 ? undefined : hovered.x + 10,
              padding: '6px 8px',
              pointerEvents: 'none',
              position: 'absolute',
              right: hovered.x > width / 2 ? width - hovered.x + 10 : undefined,
              top: 4,
              whiteSpace: 'nowrap',
              zIndex: 2,
            }}
          >
            <Text style={{ fontSize: 12 }} type="secondary">
              {dayjs(hovered.at).format('HH:mm:ss')}
            </Text>
            {hovered.values.map((entry) => (
              <Flex align="center" gap={6} key={entry.label} style={{ marginTop: 2 }}>
                <span style={{ background: entry.color, borderRadius: 2, height: 8, width: 8 }} />
                <Text style={{ fontSize: 12 }}>{entry.label}</Text>
                <Text strong style={{ fontSize: 12 }}>
                  {formatMetricValue(entry.value, unit)}
                </Text>
              </Flex>
            ))}
          </div>
        )}
      </div>

      <Text style={{ display: 'block', fontSize: 12, marginTop: 10 }} type="secondary">
        {footer.join(' · ')}
      </Text>
      {note && (
        <Text style={{ display: 'block', fontSize: 12, marginTop: 4 }} type="secondary">
          {note}
        </Text>
      )}
    </Card>
  );
}
