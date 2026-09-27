/**
 * `#/design/map` — the whole registry as cards (ticket 13).
 *
 * The third surface, and the smallest: the board with the LOD floor above
 * `maxZoom`, so every scene is its name card at every zoom and no live content
 * can ever mount. That is the entire trick — the engine already has the
 * rendering this view needs, so the map adds no node type, no layout and no
 * canvas code; it adds the reachability panel beside it.
 *
 * Bare, like the board: a canvas fills the viewport, and the shell would only
 * compete with it. The panel answers what `orphans.ts` can honestly claim —
 * overlays with no trigger (the actionable list), everything else with no link
 * in (large by design: routes and menu items are not links), and the groups no
 * menu item names.
 */

import { ExportOutlined } from '@ant-design/icons';
import { Flex, Tag, Tooltip, Typography, theme } from 'antd';
import { useCallback, useMemo, useState } from 'react';
import type { CSSProperties, ReactNode } from 'react';

import { navigate } from '../app/router';
import { boardGroups, boardMenu, toBoardScenes } from '../scenes/adapter';
import { orphanReport } from '../scenes/orphans';
import { SCENES } from '../scenes/registry';
import { Board } from '../storyboard';

const { Text } = Typography;

const CODE: CSSProperties = {
  fontFamily: "'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace",
  fontSize: 12,
};

/** Nothing here is ever rendered — the floor below keeps every frame a card. */
const noFrame = (): ReactNode => null;

const MENU_GROUPS = new Set(boardMenu.map((item) => item.targetGroupId));

export function MapPage() {
  const { token } = theme.useToken();
  const scenes = useMemo(() => toBoardScenes(SCENES), []);
  const report = useMemo(() => orphanReport(SCENES, MENU_GROUPS), []);
  const [mounted, setMounted] = useState({ mounted: 0, total: SCENES.length });
  const onMountStats = useCallback((count: number, total: number) => setMounted({ mounted: count, total }), []);

  return (
    <Flex style={{ background: token.colorBgLayout, height: '100vh' }} vertical>
      <Flex
        align="center"
        gap={16}
        style={{
          background: token.colorBgContainer,
          borderBottom: `1px solid ${token.colorBorderSecondary}`,
          flex: 'none',
          minHeight: 56,
          padding: '8px 20px',
        }}
        wrap="wrap"
      >
        <Flex align="center" gap={10}>
          <Text strong>Scene map</Text>
          <Text style={{ fontSize: 12 }} type="secondary">
            every scene as a card; no live frames at any zoom
          </Text>
        </Flex>
        <Tooltip title="Mounted cards / scenes — the proof that this canvas is cards only.">
          <Tag data-map-mounted style={{ ...CODE, marginInlineEnd: 0 }}>
            {mounted.mounted}/{mounted.total} cards
          </Tag>
        </Tooltip>
        <Flex align="center" gap={12} style={{ marginInlineStart: 'auto' }}>
          <a href="#/design/flow" onClick={(event) => { event.preventDefault(); navigate('#/design/flow'); }}>
            Task flows <ExportOutlined style={{ fontSize: 10 }} />
          </a>
          <a href="#/design/board" onClick={(event) => { event.preventDefault(); navigate('#/design/board'); }}>
            Still board <ExportOutlined style={{ fontSize: 10 }} />
          </a>
          <Text style={{ fontSize: 12 }} type="secondary">
            wheel pans · ctrl/⌘+wheel zooms
          </Text>
        </Flex>
      </Flex>

      <Flex style={{ flex: 1, minHeight: 0 }}>
        <Flex
          data-map-report="true"
          style={{
            background: token.colorBgContainer,
            borderInlineEnd: `1px solid ${token.colorBorderSecondary}`,
            flex: 'none',
            overflowY: 'auto',
            padding: '14px 16px',
            width: 320,
          }}
          vertical
        >
          <Text strong style={{ fontSize: 13 }}>
            Reachability
          </Text>
          <Text style={{ fontSize: 12, marginTop: 6 }} type="secondary">
            Links are <Text code>opens</Text> triggers and <Text code>tab</Text> panes — not routes and not menu
            items, so “no link in” is not the same as unreachable.
          </Text>

          {report.unlinkedOverlays.length === 0 ? (
            <Tag color="success" style={{ marginTop: 12, whiteSpace: 'normal' }}>
              Every overlay has a declared trigger
            </Tag>
          ) : (
            <Flex style={{ marginTop: 12 }} vertical>
              <Text strong style={{ color: token.colorError, fontSize: 12 }}>
                Overlays nothing triggers
              </Text>
              {report.unlinkedOverlays.map((row) => (
                <Text key={row.sceneId} style={{ ...CODE, fontSize: 11 }}>
                  {row.sceneId}
                </Text>
              ))}
            </Flex>
          )}

          <Text strong style={{ fontSize: 12, marginTop: 16 }}>
            No link in ({report.noIncomingLink.length})
          </Text>
          <Text style={{ fontSize: 11, marginTop: 2 }} type="secondary">
            Entry screens are reached by their route or the sider; a screen’s forced states are states of a screen,
            not destinations.
          </Text>
          <div style={{ marginTop: 8 }}>
            {report.noIncomingLink.map((row) => (
              <Flex gap={6} key={row.sceneId} style={{ lineHeight: '18px' }}>
                <Text style={{ ...CODE, color: token.colorTextTertiary, fontSize: 11, width: 128 }}>{row.group}</Text>
                <Text style={{ ...CODE, fontSize: 11 }}>{row.sceneId}</Text>
              </Flex>
            ))}
          </div>

          <Text strong style={{ fontSize: 12, marginTop: 16 }}>
            No menu item ({report.groupsWithoutMenu.length})
          </Text>
          <Text style={{ fontSize: 11, marginTop: 2 }} type="secondary">
            Reached by drilling into a screen, exactly as the sider behaves — this list is by design.
          </Text>
          <Flex gap={6} style={{ marginTop: 8 }} wrap>
            {report.groupsWithoutMenu.map((group) => (
              <Tag key={group} style={{ fontSize: 11, marginInlineEnd: 0 }}>
                {boardGroups.find((candidate) => candidate.id === group)?.label ?? group}
              </Tag>
            ))}
          </Flex>
        </Flex>

        <div style={{ flex: 1, minWidth: 0 }}>
          <Board
            groups={boardGroups}
            lodBreak={Number.POSITIVE_INFINITY}
            menu={boardMenu}
            onMountStats={onMountStats}
            renderFrame={noFrame}
            scenes={scenes}
          />
        </div>
      </Flex>
    </Flex>
  );
}
