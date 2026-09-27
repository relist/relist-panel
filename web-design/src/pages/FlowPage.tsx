/**
 * `#/design/flow` — the registry's tasks as journeys (ticket 11).
 *
 * A document, not a canvas, and deliberately inside `AppShell`: the board is
 * the place to look at frames, this is the place to read how a walker gets from
 * one to the next. One section per task; a card per step; a connector per hop
 * carrying the label of the thing that causes it — the registry link's own
 * label for a declared link, the named control for a menu/route hop.
 *
 * Plain DOM and SVG on purpose. No React Flow, no live frames: fifteen small
 * cards cannot be janky, and a diagram a reviewer can read at a glance is the
 * entire point of splitting this out of the board.
 *
 * The layout is `computeFlows` (`src/flow/layout.ts`, render-free and
 * `node --test`-covered); this file is only painting. `validateTasks` runs on
 * render as a loud guard — the test already proves it empty, so a red block
 * here means a task drifted between the test run and the page.
 */

import { ExportOutlined } from '@ant-design/icons';
import { Alert, Flex, Tag, Tooltip, Typography, theme } from 'antd';
import { useMemo, useState } from 'react';
import type { CSSProperties, MouseEvent } from 'react';

import { CODE_FONT_STACK, useThemeModeControl } from '../app/theme';
import { computeFlows } from '../flow/layout';
import type { FlowCard, FlowConnector, FlowSection } from '../flow/layout';
import { boardSceneUrl, openLiveUrl } from '../scenes/openLive';
import { SCENES } from '../scenes/registry';
import { TASKS, validateTasks } from '../scenes/tasks';
import type { Scene } from '../scenes/types';
import { groupColor } from '../storyboard/layout';

/** The board's own line colours, so the legend transfers between the surfaces. */
const CONNECTOR_COLOR: Record<FlowConnector['action'], string> = {
  opens: '#d97706',
  tab: '#64748b',
  nav: '#475569',
};

/** `opens` keeps the board's dashes; `nav` is dotted, the one kind with no link. */
const CONNECTOR_DASH: Record<FlowConnector['action'], string | undefined> = {
  opens: '7 5',
  tab: undefined,
  nav: '2 4',
};

const REGISTRY_BY_ID = new Map<string, Scene>(SCENES.map((scene) => [scene.id, scene]));

const NOTE_CLAMP: CSSProperties = {
  WebkitBoxOrient: 'vertical',
  WebkitLineClamp: 2,
  display: '-webkit-box',
  fontSize: 11,
  lineHeight: 1.35,
  overflow: 'hidden',
};

/** One step card: number, title, id, the reviewer note, and the two ways out. */
function StepCard({
  card,
  dark,
  hovered,
  dimmed,
  onHover,
}: {
  card: FlowCard;
  dark: boolean;
  hovered: boolean;
  dimmed: boolean;
  onHover: (sceneId: string | null) => void;
}) {
  const { token } = theme.useToken();
  const scene = REGISTRY_BY_ID.get(card.sceneId);
  const group = groupColor(card.group);

  const openBoard = (event: MouseEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest('a') !== null) return;
    window.open(boardSceneUrl(card.sceneId), '_blank', 'noopener');
  };

  return (
    <div
      data-flow-card={card.sceneId}
      onClick={openBoard}
      onMouseEnter={() => onHover(card.sceneId)}
      onMouseLeave={() => onHover(null)}
      style={{
        background: token.colorBgContainer,
        border: `1px solid ${hovered ? token.colorPrimary : token.colorBorderSecondary}`,
        borderRadius: 10,
        borderTop: `3px solid ${group}`,
        boxShadow: hovered ? token.boxShadowSecondary : token.boxShadowTertiary,
        boxSizing: 'border-box',
        cursor: 'pointer',
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        height: card.height,
        left: card.x,
        opacity: dimmed ? 0.35 : 1,
        padding: '9px 12px 8px',
        position: 'absolute',
        top: card.y,
        transition: 'opacity 140ms ease, border-color 120ms ease, box-shadow 120ms ease',
        width: card.width,
      }}
    >
      <Flex align="center" gap={8}>
        <span
          style={{
            alignItems: 'center',
            background: group,
            borderRadius: '50%',
            color: '#ffffff',
            display: 'inline-flex',
            flex: 'none',
            fontSize: 11,
            fontWeight: 600,
            height: 18,
            justifyContent: 'center',
            width: 18,
          }}
        >
          {card.step}
        </span>
        <Typography.Text strong ellipsis style={{ fontSize: 13, lineHeight: 1.25 }}>
          {card.title}
        </Typography.Text>
        {card.kind !== 'screen' ? (
          <Tag color="default" style={{ flex: 'none', fontSize: 10, lineHeight: '14px', marginInlineEnd: 0, padding: '0 4px' }}>
            {card.kind}
          </Tag>
        ) : null}
      </Flex>

      <Typography.Text code style={{ color: token.colorTextTertiary, fontSize: 10, lineHeight: 1.2 }}>
        {card.sceneId}
      </Typography.Text>

      {card.note !== undefined ? (
        <div style={{ ...NOTE_CLAMP, color: token.colorTextSecondary }} title={card.note}>
          {card.note}
        </div>
      ) : null}

      <Flex gap={12} style={{ fontSize: 11, marginTop: 'auto' }}>
        <a href={boardSceneUrl(card.sceneId)} rel="noreferrer" target="_blank">
          Board <ExportOutlined style={{ fontSize: 9 }} />
        </a>
        {scene !== undefined ? (
          <a href={openLiveUrl(scene, dark)} rel="noreferrer" target="_blank">
            Live <ExportOutlined style={{ fontSize: 9 }} />
          </a>
        ) : null}
      </Flex>
    </div>
  );
}

/** A link hop: a line in the gap with its label above it. */
function ConnectorLine({
  connector,
  active,
  dimmed,
}: {
  connector: FlowConnector;
  active: boolean;
  dimmed: boolean;
}) {
  const { token } = theme.useToken();
  const color = CONNECTOR_COLOR[connector.action];
  const middleX = (connector.x1 + connector.x2) / 2;
  const middleY = (connector.y1 + connector.y2) / 2;

  return (
    <g data-flow-connector={`${connector.from}->${connector.to}`} opacity={dimmed ? 0.25 : 1}>
      <line
        markerEnd={`url(#flow-arrow-${connector.action})`}
        stroke={color}
        strokeDasharray={CONNECTOR_DASH[connector.action]}
        strokeWidth={active ? 2 : 1.4}
        x1={connector.x1}
        x2={connector.x2}
        y1={connector.y1}
        y2={connector.y2}
      />
      <foreignObject height={44} width={connector.x2 - connector.x1} x={middleX - (connector.x2 - connector.x1) / 2} y={middleY - 46}>
        <div
          style={{
            color: token.colorTextSecondary,
            fontSize: 10,
            lineHeight: 1.25,
            maxHeight: 42,
            overflow: 'hidden',
            textAlign: 'center',
            wordBreak: 'break-word',
          }}
          title={connector.label}
        >
          {connector.label}
        </div>
      </foreignObject>
    </g>
  );
}

/** A cross-row hop is a chip under the row it leaves, never a line across it. */
function WrapChip({ connector }: { connector: FlowConnector }) {
  const { token } = theme.useToken();
  return (
    <div
      data-flow-wrap={`${connector.from}->${connector.to}`}
      style={{
        alignItems: 'center',
        background: token.colorFillQuaternary,
        border: `1px dashed ${token.colorBorder}`,
        borderRadius: 999,
        color: token.colorTextSecondary,
        display: 'inline-flex',
        fontSize: 11,
        gap: 6,
        left: connector.x1,
        maxWidth: 260,
        padding: '2px 10px',
        position: 'absolute',
        top: connector.y1,
      }}
      title={connector.label}
    >
      <span style={{ color: CONNECTOR_COLOR[connector.action] }}>↓</span>
      <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{connector.label}</span>
    </div>
  );
}

function FlowSectionView({
  section,
  dark,
  hovered,
  onHover,
}: {
  section: FlowSection;
  dark: boolean;
  hovered: { taskId: string; sceneId: string } | null;
  onHover: (hover: { taskId: string; sceneId: string } | null) => void;
}) {
  const { token } = theme.useToken();
  const inThisTask = hovered?.taskId === section.taskId;
  const activeScene = inThisTask ? hovered.sceneId : null;

  const onCardHover = (taskId: string) => (sceneId: string | null) =>
    onHover(sceneId === null ? null : { taskId, sceneId });

  return (
    <section
      data-flow-task={section.taskId}
      style={{ opacity: hovered !== null && !inThisTask ? 0.35 : 1, transition: 'opacity 160ms ease' }}
    >
      <Flex align="baseline" gap={12} wrap>
        <Typography.Text strong style={{ fontSize: 15 }}>
          {section.label}
        </Typography.Text>
        <Typography.Text style={{ fontSize: 13 }} type="secondary">
          {section.summary}
        </Typography.Text>
        <Tag style={{ marginInlineStart: 'auto' }}>{section.cards.length} steps</Tag>
      </Flex>

      <div style={{ marginTop: 14, overflowX: 'auto' }}>
        <div style={{ height: section.height, position: 'relative', width: section.width }}>
          <svg
            height={section.height}
            style={{ left: 0, overflow: 'visible', pointerEvents: 'none', position: 'absolute', top: 0 }}
            width={section.width}
          >
            <defs>
              {(Object.keys(CONNECTOR_COLOR) as FlowConnector['action'][]).map((action) => (
                <marker
                  id={`flow-arrow-${action}`}
                  key={action}
                  markerHeight="6"
                  markerWidth="6"
                  orient="auto"
                  refX="5"
                  refY="3"
                >
                  <path d="M 0 0 L 6 3 L 0 6 z" fill={CONNECTOR_COLOR[action]} />
                </marker>
              ))}
            </defs>
            {section.connectors
              .filter((connector) => !connector.wrap)
              .map((connector) => (
                <ConnectorLine
                  active={activeScene !== null && (connector.from === activeScene || connector.to === activeScene)}
                  connector={connector}
                  dimmed={
                    inThisTask && activeScene !== null && connector.from !== activeScene && connector.to !== activeScene
                  }
                  key={`${connector.from}->${connector.to}`}
                />
              ))}
          </svg>

          {section.connectors
            .filter((connector) => connector.wrap)
            .map((connector) => (
              <WrapChip connector={connector} key={`${connector.from}->${connector.to}`} />
            ))}

          {section.cards.map((card) => (
            <StepCard
              card={card}
              dark={dark}
              dimmed={inThisTask && activeScene !== null && activeScene !== card.sceneId}
              hovered={activeScene === card.sceneId}
              key={card.sceneId}
              onHover={onCardHover(section.taskId)}
            />
          ))}
        </div>
      </div>
    </section>
  );
}

export function FlowPage() {
  const { token } = theme.useToken();
  const { mode } = useThemeModeControl();
  const problems = useMemo(() => validateTasks(TASKS, SCENES), []);
  const sections = useMemo(() => computeFlows(TASKS, SCENES), []);
  const [hovered, setHovered] = useState<{ taskId: string; sceneId: string } | null>(null);

  return (
    <Flex gap={token.marginXL} vertical>
      <Flex align="flex-start" gap={16} justify="space-between" wrap>
        <div>
          <Typography.Title level={3} style={{ margin: 0 }}>
            Task flows
          </Typography.Title>
          <Typography.Text style={{ display: 'block', marginTop: 6, maxWidth: 760 }} type="secondary">
            The journeys the registry declares, one row per task. Every connector carries the label of the thing that
            causes it — a registry link's own label, or the menu control a hop goes through — so a renamed link moves
            the diagram with it. Hover a card to light its hops; open a frame on the board or in the live mock.
          </Typography.Text>
        </div>

        {/* The legend names the three line kinds; the board's legend names two. */}
        <Flex align="center" gap={14} style={{ flexWrap: 'wrap' }} data-flow-legend="true">
          <Flex align="center" gap={6}>
            <svg aria-hidden height="6" width="26">
              <line stroke={CONNECTOR_COLOR.tab} strokeWidth="2" x1="0" x2="26" y1="3" y2="3" />
            </svg>
            <Typography.Text style={{ fontSize: 12 }} type="secondary">
              tab
            </Typography.Text>
          </Flex>
          <Flex align="center" gap={6}>
            <svg aria-hidden height="6" width="26">
              <line stroke={CONNECTOR_COLOR.opens} strokeDasharray="4 3" strokeWidth="2" x1="0" x2="26" y1="3" y2="3" />
            </svg>
            <Typography.Text style={{ fontSize: 12 }} type="secondary">
              opens — trigger → overlay
            </Typography.Text>
          </Flex>
          <Flex align="center" gap={6}>
            <svg aria-hidden height="6" width="26">
              <line stroke={CONNECTOR_COLOR.nav} strokeDasharray="1 3" strokeLinecap="round" strokeWidth="2" x1="0" x2="26" y1="3" y2="3" />
            </svg>
            <Typography.Text style={{ fontSize: 12 }} type="secondary">
              nav — menu &amp; routes
            </Typography.Text>
          </Flex>
        </Flex>
      </Flex>

      {problems.length > 0 ? (
        <Alert
          description={
            <ul style={{ margin: 0, paddingInlineStart: 20 }}>
              {problems.map((problem) => (
                <li key={`${problem.taskId}-${problem.kind}-${problem.detail}`}>
                  <Typography.Text code>{problem.taskId}</Typography.Text> — {problem.detail}
                </li>
              ))}
            </ul>
          }
          message={`${problems.length} task problem${problems.length === 1 ? '' : 's'} — the registry and the tasks disagree`}
          showIcon
          type="error"
        />
      ) : null}

      {sections.map((section) => (
        <FlowSectionView
          dark={mode === 'dark'}
          hovered={hovered}
          key={section.taskId}
          onHover={setHovered}
          section={section}
        />
      ))}

      <Typography.Text style={{ color: token.colorTextTertiary, fontFamily: CODE_FONT_STACK, fontSize: 11 }}>
        src/scenes/tasks.ts · {TASKS.length} tasks · {sections.reduce((count, section) => count + section.cards.length, 0)} steps
      </Typography.Text>
    </Flex>
  );
}
