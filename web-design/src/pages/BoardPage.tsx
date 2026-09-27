import { BulbOutlined, ExportOutlined, MoonOutlined } from '@ant-design/icons';
import { Flex, Input, Segmented, Select, Switch, Tag, Tooltip, Typography, theme } from 'antd';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties, ReactNode } from 'react';

import { AppShell } from '../app/AppShell';
import { parse } from '../app/router';
import { Screens } from '../app/Screens';
import { CODE_FONT_STACK, useThemeModeControl } from '../app/theme';
import { boardGroups, boardMenu, sceneOverridesOf, toBoardScenes, toUiValue } from '../scenes/adapter';
import { openLiveUrl } from '../scenes/openLive';
import { SCENES } from '../scenes/registry';
import { SceneScopeProvider } from '../scenes/scope';
import type { Scene, SceneGroupId } from '../scenes/types';
import { Board, DEFAULT_NAV_COLOR, DEFAULT_OPENS_COLOR } from '../storyboard';
import type { BoardHandle, BoardScene } from '../storyboard';
import type { SceneOverrides } from '../data/store';

const { Text } = Typography;

/**
 * The still board (`#/design/board`, tickets 06/07/08) — the registry rendered
 * as one pan/zoom canvas of live frames, outside `AppShell` (the shell is part
 * of what each frame is showing).
 *
 * Division of labour: the engine (`src/storyboard`) owns the canvas, the
 * layout and the culling; the adapter (`src/scenes/adapter`) owns the registry
 * → engine mapping; this page owns the chrome. The frame itself is
 * `SceneScopeProvider` + the app's own screens, so a frame is the running mock
 * at a forced state — not a picture of it — and the theme switch re-themes
 * chrome and frames at once through the same context `Root` provides.
 *
 * Frame chrome lives *inside* the engine's frame box, under the scope: the
 * mapped frame is `scene.size` plus a chrome strip, and the strip takes
 * `pointer-events` back (the engine's frame box is static, but its chrome is
 * the board's own UI — copyable id, *Open live*). The engine renders frames;
 * nothing here wraps or observes frame content.
 */

/** Height of the chrome strip under each frame; the mapped frame reserves it. */
const CHROME_HEIGHT = 80;

const noop = (): void => {};

/** Registry lookups, computed once — the registry is a static module. */
const REGISTRY_BY_ID = new Map<string, Scene>(SCENES.map((scene) => [scene.id, scene]));
const ALL_BOARD_SCENES: BoardScene[] = toBoardScenes(SCENES);
const UI_MAPS = new Map<string, ReadonlyMap<string, ReturnType<typeof toUiValue>>>(
  SCENES.map((scene) => [
    scene.id,
    new Map(Object.entries(scene.ui ?? {}).map(([key, value]) => [key, toUiValue(value)])),
  ]),
);
const OVERRIDES = new Map<string, SceneOverrides>(
  SCENES.map((scene) => [scene.id, sceneOverridesOf(UI_MAPS.get(scene.id) ?? new Map())]),
);
const EMPTY_MENU = boardMenu.slice(0, 0);

/** Auth screens render without the shell, exactly as `Root` renders them. */
function isBareHash(hash: string): boolean {
  const kind = parse(hash).kind;
  return kind === 'signin' || kind === 'setup';
}

/**
 * The *Open live* URL moved to `../scenes/openLive` when the task flow page
 * needed the same rule (ticket 11). Board chrome calls it with the board's own
 * dark state.
 */

const CAPTION_CLAMP: CSSProperties = {
  WebkitBoxOrient: 'vertical',
  WebkitLineClamp: 2,
  display: '-webkit-box',
  overflow: 'hidden',
};

/** The strip under a frame: id (copyable), title, route, caption, *Open live*. */
function FrameChrome({ dark, scene }: { dark: boolean; scene: Scene }) {
  const { token } = theme.useToken();

  return (
    <div
      style={{
        background: token.colorBgContainer,
        boxSizing: 'border-box',
        // The engine's frame box is `pointer-events: none` (a frame is static);
        // this strip is the board's own chrome, so it takes its events back.
        minHeight: 0,
        overflow: 'hidden',
        padding: '8px 14px 10px',
        pointerEvents: 'auto',
        width: '100%',
      }}
    >
      <Flex align="center" gap={10}>
        <Typography.Text code copyable={{ text: scene.id }} style={{ fontSize: 12 }}>
          {scene.id}
        </Typography.Text>
        <Text strong style={{ fontSize: 12 }}>
          {scene.title}
        </Text>
        <Text code style={{ color: token.colorTextTertiary, fontSize: 11 }}>
          {scene.hash}
        </Text>
        <a
          href={openLiveUrl(scene, dark)}
          rel="noreferrer"
          style={{ fontSize: 12, marginInlineStart: 'auto', whiteSpace: 'nowrap' }}
          target="_blank"
        >
          <ExportOutlined /> Open live
        </a>
      </Flex>
      <div style={CAPTION_CLAMP}>
        <Text style={{ color: token.colorTextSecondary, fontSize: 12 }}>{scene.caption}</Text>
      </div>
    </div>
  );
}

export function BoardPage() {
  const { token } = theme.useToken();
  const { mode, setMode } = useThemeModeControl();

  const boardRef = useRef<BoardHandle>(null);
  const [captions, setCaptions] = useState(true);
  const [groupFilter, setGroupFilter] = useState<SceneGroupId | 'all'>('all');
  // Off by default (ticket 12): reading a journey is #/design/flow's job now,
  // and an inventory with its lines off is the board's best state.
  const [lines, setLines] = useState(false);
  const [stats, setStats] = useState({ mounted: 0, total: SCENES.length });
  // `?scene=<id>` deep link. The box *reflects* the id, but the deep link is a
  // jump, not a filter — filtering by it would collapse the board to one scene
  // exactly when a reviewer wants the frame in its context. The filter only
  // follows the box once it is edited.
  const deepSceneId = useMemo(() => new URLSearchParams(window.location.search).get('scene'), []);
  const [query, setQuery] = useState(deepSceneId ?? '');
  const [filter, setFilter] = useState('');

  const toggleTheme = useCallback(() => setMode(mode === 'dark' ? 'light' : 'dark'), [mode, setMode]);
  const onMountStats = useCallback((mounted: number, total: number) => setStats({ mounted, total }), []);

  /**
   * The deep link's camera move. The engine's own `initialSceneId` effect can
   * fire before the canvas has its camera and drop the `setCenter` silently,
   * so the jump goes through the public handle once the canvas has settled
   * (`focusScene` is idempotent — the engine's `initialSceneId` stays on and
   * doubles the call harmlessly when it does fire).
   */
  useEffect(() => {
    if (deepSceneId === null) return undefined;
    const timer = window.setTimeout(() => boardRef.current?.focusScene(deepSceneId), 400);
    return () => window.clearTimeout(timer);
  }, [deepSceneId]);

  /** Filtered view of the registry — search by id or title, plus one group. */
  const { scenes, groups } = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const visible = SCENES.filter((scene) => {
      if (groupFilter !== 'all' && scene.group !== groupFilter) return false;
      if (q !== '' && !scene.id.toLowerCase().includes(q) && !scene.title.toLowerCase().includes(q)) return false;
      return true;
    });
    const sceneIds = new Set(visible.map((scene) => scene.id));
    const groupIds = new Set<string>(visible.map((scene) => scene.group));
    return {
      groups: boardGroups.filter((group) => groupIds.has(group.id)),
      scenes: ALL_BOARD_SCENES.filter((frame) => sceneIds.has(frame.id)),
    };
  }, [groupFilter, query]);

  /**
   * What the engine actually receives: the chrome strip added to every mapped
   * size while captions are on, and — with the lines layer off — no `opens`
   * triggers, no `tabs` and an empty menu, so the layout declares no edges at
   * all (the engine takes edges from the mapping; a dangling target is never
   * drawn, and a filtered-out scene drops its connectors with it).
   */
  const layoutScenes = useMemo(
    () =>
      scenes.map((frame) => {
        const size = {
          width: frame.size.width,
          height: frame.size.height + (captions ? CHROME_HEIGHT : 0),
        };
        if (!lines) {
          const { opens: _opens, tabs: _tabs, ...rest } = frame;
          return { ...rest, size };
        }
        return { ...frame, size };
      }),
    [captions, lines, scenes],
  );
  const layoutMenu = lines ? boardMenu : EMPTY_MENU;

  /** Enter in the search box centers the first match instead of only filtering. */
  const jumpToQuery = () => {
    const q = query.trim().toLowerCase();
    if (q === '') return;
    const hit = scenes.find((frame) => frame.id.toLowerCase().includes(q) || frame.title.toLowerCase().includes(q));
    if (hit !== undefined) boardRef.current?.focusScene(hit.id);
  };

  /**
   * One frame of the board: the app's own screens under a scene scope (route,
   * forced `ui`, `after:` transition, store seed, frame-local toasts, portals,
   * error boundary — ticket 10), plus the chrome strip below them. Auth scenes
   * render bare, every other scene renders the shell, exactly as `Root` does.
   */
  const renderFrame = useCallback(
    (frame: BoardScene): ReactNode => {
      const scene = REGISTRY_BY_ID.get(frame.id);
      if (scene === undefined) return null;

      return (
        <Flex style={{ height: '100%' }} vertical>
          <SceneScopeProvider
            after={scene.after}
            height={scene.size.height}
            id={scene.id}
            overrides={OVERRIDES.get(scene.id)}
            route={scene.hash}
            ui={UI_MAPS.get(scene.id)}
            width={scene.size.width}
          >
            {isBareHash(scene.hash) ? (
              <Screens onSignIn={noop} />
            ) : (
              <AppShell onToggleTheme={toggleTheme} themeMode={mode}>
                <Screens onSignIn={noop} />
              </AppShell>
            )}
          </SceneScopeProvider>
          {captions && <FrameChrome dark={mode === 'dark'} scene={scene} />}
        </Flex>
      );
    },
    [captions, mode, toggleTheme],
  );

  const groupLabel = (id: SceneGroupId | 'all'): string =>
    id === 'all' ? 'all groups' : (boardGroups.find((group) => group.id === id)?.label ?? id);

  return (
    <Flex style={{ background: token.colorBgLayout, height: '100vh' }} vertical>
      {/* The sticky chrome bar — outside the canvas transform, so controls stay
          readable at any zoom. Its state is view state of the board, not of the
          mock: nothing here is spelled into `?ui=`. */}
      <Flex
        align="center"
        data-board-chrome="true"
        gap={16}
        style={{
          background: token.colorBgContainer,
          borderBottom: `1px solid ${token.colorBorderSecondary}`,
          flex: 'none',
          minHeight: 56,
          padding: '8px 20px',
          position: 'sticky',
          top: 0,
          zIndex: 20,
        }}
        wrap="wrap"
      >
        <Flex align="center" gap={10}>
          <Text strong>Still board</Text>
          <Text style={{ fontSize: 12 }} type="secondary">
            {scenes.length}/{SCENES.length} scenes
          </Text>
          <Tag data-board-mounted style={{ fontFamily: CODE_FONT_STACK, marginInlineEnd: 0 }}>
            {stats.mounted}/{stats.total} mounted
          </Tag>
        </Flex>

        <Input.Search
          allowClear
          enterButton="Jump"
          onChange={(event) => {
            setQuery(event.target.value);
            setFilter(event.target.value);
          }}
          onSearch={jumpToQuery}
          placeholder="Filter by id or title — Enter jumps"
          size="small"
          style={{ width: 260 }}
          value={query}
        />

        <Select<SceneGroupId | 'all'>
          onChange={setGroupFilter}
          options={[{ label: 'All groups', value: 'all' }, ...boardGroups.map((group) => ({ label: group.label, value: group.id }))]}
          size="small"
          style={{ minWidth: 160 }}
          value={groupFilter}
        />

        <Flex align="center" gap={8}>
          <Text style={{ fontSize: 12 }} type="secondary">
            Theme
          </Text>
          {/* Flips chrome and every live frame at once: everything below reads
              the mode through the same contexts `Root` provides. */}
          <Flex align="center" data-board-theme="true" gap={6}>
            {mode === 'light' ? (
              <BulbOutlined style={{ color: token.colorTextTertiary }} />
            ) : (
              <MoonOutlined style={{ color: token.colorTextTertiary }} />
            )}
            <Switch
              checked={mode === 'dark'}
              checkedChildren={<MoonOutlined />}
              onChange={toggleTheme}
              size="small"
              unCheckedChildren={<BulbOutlined />}
            />
          </Flex>
        </Flex>

        {/* Desktop is the only designed width until ticket 03 lands; the switch
            is an honest placeholder, not a fake re-layout. */}
        <Tooltip title="Tablet (834) and Mobile (390) are designed in ticket 03 — the board runs desktop until then.">
          <Segmented
            options={[
              { label: 'Desktop', value: 'desktop' },
              { disabled: true, label: 'Tablet', value: 'tablet' },
              { disabled: true, label: 'Mobile', value: 'mobile' },
            ]}
            size="small"
            value="desktop"
          />
        </Tooltip>

        <Flex align="center" gap={12}>
          <Flex align="center" gap={6}>
            <Text style={{ fontSize: 12 }} type="secondary">
              Lines
            </Text>
            <Switch checked={lines} onChange={setLines} size="small" />
          </Flex>
          <Flex align="center" gap={6}>
            <Text style={{ fontSize: 12 }} type="secondary">
              Captions
            </Text>
            <Switch checked={captions} onChange={setCaptions} size="small" />
          </Flex>
        </Flex>

        {/* The legend names the only two line kinds (ticket 07). */}
        <Flex align="center" data-board-legend="true" gap={6}>
          <svg aria-hidden height="6" width="26">
            <line stroke={DEFAULT_NAV_COLOR} strokeWidth="2" x1="0" x2="26" y1="3" y2="3" />
          </svg>
          <Text style={{ fontSize: 12 }} type="secondary">
            nav — menu &amp; tabs
          </Text>
          <svg aria-hidden height="6" width="26">
            <line stroke={DEFAULT_OPENS_COLOR} strokeDasharray="4 3" strokeWidth="2" x1="0" x2="26" y1="3" y2="3" />
          </svg>
          <Text style={{ fontSize: 12 }} type="secondary">
            opens — trigger → overlay
          </Text>
        </Flex>

        <Text style={{ fontSize: 12, marginInlineStart: 'auto' }} type="secondary">
          wheel pans · ctrl/⌘+wheel zooms · drag pans · zoom &amp; fit: controls, bottom-left
        </Text>
      </Flex>

      <div style={{ flex: 1, minHeight: 0 }}>
        {scenes.length === 0 ? (
          <Flex align="center" justify="center" style={{ height: '100%' }}>
            <Text type="secondary">
              No scene matches “{query.trim()}” in {groupLabel(groupFilter)} — clear the search or pick another group.
            </Text>
          </Flex>
        ) : (
          <Board
            groups={groups}
            initialSceneId={deepSceneId ?? undefined}
            menu={layoutMenu}
            onMountStats={onMountStats}
            ref={boardRef}
            renderFrame={renderFrame}
            scenes={layoutScenes}
          />
        )}
      </div>
    </Flex>
  );
}
