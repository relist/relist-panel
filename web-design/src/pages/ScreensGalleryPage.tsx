import { ExportOutlined, ReloadOutlined } from '@ant-design/icons';
import { Button, Divider, Flex, Segmented, Switch, Tag, Typography, theme } from 'antd';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { paths } from '../app/router';
import { CODE_FONT_STACK, LAYOUT, useThemeModeControl } from '../app/theme';

const { Text, Title } = Typography;

/**
 * Every screen of the panel on one page, each in its own live frame — the
 * review surface this design pass is agreed from.
 *
 * Four decisions hold the shape together, and each one has a cheaper-looking
 * alternative that does not survive contact with the panel:
 *
 *  - **A frame is an iframe, not the page component rendered inline.** Inline
 *    rendering would mean faking route params and hand-wiring the sider for
 *    every frame. An iframe is the real `Root` at a real route: the shell, the
 *    store and antd's own layout all render exactly as they do in the product,
 *    and the frames stay interactive.
 *  - **Frames are a fixed viewport, not content-height.** `LAYOUT.editorMinHeight`
 *    is `clamp(640px, 80vh, 1000px)` — inside a frame, `vh` resolves against the
 *    frame. Sizing the frame from its content would make the height depend on the
 *    content and the content on the height, and the composition a reviewer
 *    approved would be one no real viewport ever shows.
 *  - **Zoom is `transform: scale()`, not a narrower frame.** Scaling leaves the
 *    layout viewport at 1600px, so what is on screen is the real desktop layout
 *    drawn smaller. Narrowing the frame would review a width the panel is not
 *    designed for.
 *  - **Jump chips are buttons, not `href="#id"`.** The hash is the router; an
 *    anchor link would be parsed as a route and land on Not found.
 */

/** The width every frame renders at. A real desktop, not a resizable box. */
const FRAME_WIDTH = 1600;

const FRAME_HEIGHTS = [800, 1000, 1400];
const ZOOMS = [0.5, 0.65, 0.8, 1];

type Shot = {
  /** Written through `paths`, so a route rename moves the gallery with it. */
  hash: string;
  note: string;
  state: string;
  title: string;
};

type Group = { blurb: string; shots: Shot[]; title: string };

const GROUPS: Group[] = [
  {
    blurb: 'What the operator sees when they are not looking at any one Project.',
    title: 'Host and inventory',
    shots: [
      {
        hash: paths.overview,
        note: 'Opens with a Volume Handle already running on my-saas / production / web. The warning banner is the only control the design gives you over a handle left open, so it has to be part of the default state rather than something you have to go and create.',
        state: 'Populated · one Handle live',
        title: 'Overview',
      },
      {
        hash: paths.monitoring,
        note: 'Host totals, per-Compose sampling, and the sampler\u2019s own cost.',
        state: 'Populated',
        title: 'Monitoring',
      },
      {
        hash: paths.projects,
        note: 'The same list in both views, with the filter and the sort control.',
        state: 'Populated · cards and table',
        title: 'Projects',
      },
    ],
  },
  {
    blurb: 'One Project, its environments, and the two things you can change about it.',
    title: 'A Project',
    shots: [
      {
        hash: paths.project(1),
        note: 'The environment switcher, and the Compose list for the selected environment.',
        state: 'Composes tab',
        title: 'Project',
      },
      {
        hash: paths.environment(1, 1, 'variables'),
        note: 'Secret values masked until revealed, and the bulk paste that takes a whole .env at once.',
        state: 'Populated · masked',
        title: 'Environment Variables',
      },
      {
        hash: paths.environment(1, 1, 'settings'),
        note: 'Rename, the environment list, and the delete path that asks you to type the name.',
        state: 'Populated · danger zone',
        title: 'Project settings',
      },
    ],
  },
  {
    blurb: 'One Compose in the three lifecycle states the panel distinguishes, then its Volumes.',
    title: 'A Compose',
    shots: [
      {
        hash: paths.compose(1, 1, 1),
        note: 'Editor and status rail stretch to the same height, and the logs drawer opens over them.',
        state: 'Running · deployed',
        title: 'Compose',
      },
      {
        hash: paths.compose(1, 1, 2),
        note: 'One of two containers up, one exited service, and the attention alert that says so.',
        state: 'Partly running',
        title: 'Compose',
      },
      {
        hash: paths.compose(1, 1, 3),
        note: 'Dashed “Not deployed” tag and an empty service list — the state a Compose is in before its first deploy.',
        state: 'Never deployed',
        title: 'Compose',
      },
      {
        hash: paths.compose(1, 1, 1, 'volumes'),
        note: 'Only the Volumes this Compose declares: one named volume, two binds that resolve, one bind that is not on the host, and one that cannot be checked. One already has a live Handle.',
        state: 'Five Volumes · one Handle live',
        title: 'Compose → Volumes',
      },
      {
        hash: paths.compose(1, 1, 3, 'volumes'),
        note: 'Before the first deploy: `docker compose config` cannot find the env file, so the Volumes cannot be listed at all. This is the screen that decides what “we could not tell you” looks like.',
        state: 'Unresolvable',
        title: 'Compose → Volumes',
      },
    ],
  },
  {
    blurb: 'The file browser a Volume Handle opens, and the states it has to survive.',
    title: 'Volume Explorer',
    shots: [
      {
        hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads'),
        note: 'A symlink that stays inside the root, a symlink that escapes it, a file too large to open in the editor, and a directory the Handle is not permitted to read.',
        state: 'Root directory',
        title: 'Volume Explorer',
      },
      {
        hash: paths.volumeExplorer(1, 1, 1, '1:/app/uploads', 'invoices/2026-09'),
        note: 'The same screen one level down, with the breadcrumb trail and the parent row.',
        state: 'Subdirectory',
        title: 'Volume Explorer',
      },
    ],
  },
  {
    blurb: 'The written rules, and the component states they are drawn from.',
    title: 'Design system',
    shots: [
      {
        hash: paths.guidelines,
        note: 'The same content as web-design/GUIDELINES.md, rendered next to the screens it describes.',
        state: 'The rules, rendered',
        title: 'Guidelines',
      },
      {
        hash: paths.states,
        note: 'Loading, empty, error, feedback, destructive and narrow — the states that are easy to leave unspecified.',
        state: 'State gallery',
        title: 'States',
      },
    ],
  },
  {
    blurb: 'The screens that sit outside the shell.',
    title: 'Auth and fallback',
    shots: [
      {
        hash: paths.signIn,
        note: 'The split layout, with the panel\u2019s own mark on the right.',
        state: 'Split layout',
        title: 'Sign in',
      },
      {
        hash: paths.setup,
        note: 'First run: two steps, and no way past them until the host has an account.',
        state: 'Two-step first run',
        title: 'Setup',
      },
      {
        hash: '#/nope',
        note: 'The 404, with the routes back out of it.',
        state: 'Unknown route',
        title: 'Not found',
      },
    ],
  },
];

export function ScreensGalleryPage() {
  const { token } = theme.useToken();
  const { mode, setMode } = useThemeModeControl();

  const [frameHeight, setFrameHeight] = useState(1000);
  const [notesOpen, setNotesOpen] = useState(true);
  const [reloads, setReloads] = useState(0);
  const [zoom, setZoom] = useState(0.8);

  const frames = useRef<(HTMLIFrameElement | null)[]>([]);

  /**
   * A frame is this document at another route, so the src is derived from
   * `window.location` rather than written out: the same rule then holds on the
   * dev server, under `vite preview`, and for a `file://` open of the
   * single-file build.
   */
  const frameSrc = useCallback((hash: string) => {
    const url = new URL(window.location.href);
    url.hash = hash;
    return url.toString();
  }, []);

  /**
   * Each frame is a separate document with its own `Root`, so the mode has to be
   * pushed across. The listener lives in `main.tsx`; this side also fires on
   * every frame's `load`, because a frame that has not finished booting yet
   * never saw the last broadcast.
   */
  const syncTheme = useCallback(() => {
    for (const frame of frames.current) {
      // `*` because a `file://` document has an opaque origin, which cannot be
      // named as a target origin. The payload is one word, and the listener
      // checks its shape before it does anything with it.
      frame?.contentWindow?.postMessage({ mode, type: 'relist:theme' }, '*');
    }
  }, [mode]);

  useEffect(syncTheme, [syncTheme]);

  /** Numbered once, here, so the frame, its anchor and its jump chip cannot disagree. */
  const { groups, total } = useMemo(() => {
    let index = 0;
    const numbered = GROUPS.map((group) => ({
      ...group,
      shots: group.shots.map((shot) => ({ ...shot, index: index++ })),
    }));
    return { groups: numbered, total: index };
  }, []);

  const jump = (index: number) => {
    document.getElementById(`shot-${index}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  return (
    <div style={{ background: token.colorBgLayout, minHeight: '100vh' }}>
      <Flex
        align="center"
        gap={16}
        justify="space-between"
        style={{
          background: token.colorBgContainer,
          borderBottom: `1px solid ${token.colorBorderSecondary}`,
          minHeight: LAYOUT.barHeight,
          padding: '8px 24px',
          position: 'sticky',
          top: 0,
          zIndex: 20,
        }}
        wrap
      >
        <Flex align="center" gap={10}>
          <Text strong>Every screen</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {total} frames · {FRAME_WIDTH}px wide
          </Text>
        </Flex>
        <Flex align="center" gap={16} wrap>
          <Flex align="center" gap={8}>
            <Text type="secondary" style={{ fontSize: 12 }}>
              Theme
            </Text>
            <Segmented
              onChange={(value) => setMode(value as typeof mode)}
              options={[
                { label: 'Light', value: 'light' },
                { label: 'Dark', value: 'dark' },
              ]}
              size="small"
              value={mode}
            />
          </Flex>
          <Flex align="center" gap={8}>
            <Text type="secondary" style={{ fontSize: 12 }}>
              Viewport
            </Text>
            <Segmented
              onChange={(value) => setFrameHeight(Number(value))}
              options={FRAME_HEIGHTS.map((height) => ({ label: `${height}px`, value: height }))}
              size="small"
              value={frameHeight}
            />
          </Flex>
          <Flex align="center" gap={8}>
            <Text type="secondary" style={{ fontSize: 12 }}>
              Zoom
            </Text>
            <Segmented
              onChange={(value) => setZoom(Number(value))}
              options={ZOOMS.map((value) => ({ label: `${Math.round(value * 100)}%`, value }))}
              size="small"
              value={zoom}
            />
          </Flex>
          <Flex align="center" gap={6}>
            <Text type="secondary" style={{ fontSize: 12 }}>
              Notes
            </Text>
            <Switch checked={notesOpen} onChange={setNotesOpen} size="small" />
          </Flex>
          <Button
            icon={<ReloadOutlined />}
            onClick={() => setReloads((count) => count + 1)}
            size="small"
            title="Rebuild every frame — use it after you have changed something inside one"
          >
            Reload
          </Button>
        </Flex>
      </Flex>

      <div style={{ margin: '0 auto', maxWidth: FRAME_WIDTH * zoom + 96, padding: '32px 24px 96px' }}>
        <Title level={2} style={{ marginBottom: 8 }}>
          Relist Panel — every screen
        </Title>
        <Text type="secondary" style={{ display: 'block', maxWidth: 900 }}>
          The whole prototype on one page. Every frame below is the running app at that route, not a
          picture of it: open a drawer, switch a tab, close a Volume Handle, and the frame answers.
          The written rules are at{' '}
          <a href={paths.guidelines}>Guidelines</a>; the component states are at{' '}
          <a href={paths.states}>States</a>.
        </Text>

        <Flex gap={6} style={{ marginTop: 20 }} wrap>
          {groups.flatMap((group) =>
            group.shots.map((shot) => (
              // Five titles repeat (three Composes, two Volumes tabs, two
              // Explorers), so the chip carries the state too — otherwise the
              // jump list has entries a reviewer cannot tell apart.
              <Button
                key={shot.index}
                onClick={() => jump(shot.index)}
                size="small"
                title={`${shot.title} — ${shot.state}`}
                type="text"
              >
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {String(shot.index + 1).padStart(2, '0')}
                </Text>{' '}
                {shot.title}
              </Button>
            )),
          )}
        </Flex>

        {groups.map((group) => (
          <div key={group.title} style={{ marginTop: 40 }}>
            <Divider style={{ marginBottom: 16 }} titlePlacement="start">
              <Text strong>{group.title}</Text>
            </Divider>
            <Text type="secondary" style={{ display: 'block', marginBottom: 24 }}>
              {group.blurb}
            </Text>

            <Flex gap={56} vertical>
              {group.shots.map((shot) => (
                <section id={`shot-${shot.index}`} key={shot.index} style={{ scrollMarginTop: 88 }}>
                  <Flex align="center" gap={10} wrap>
                    <Text strong>
                      {String(shot.index + 1).padStart(2, '0')} · {shot.title}
                    </Text>
                    <Tag>{shot.state}</Tag>
                    <Text
                      style={{ color: token.colorTextTertiary, fontFamily: CODE_FONT_STACK, fontSize: 12 }}
                    >
                      {FRAME_WIDTH} × {frameHeight}
                    </Text>
                    <a href={frameSrc(shot.hash)} rel="noreferrer" target="_blank">
                      <ExportOutlined style={{ fontSize: 11 }} /> Open
                    </a>
                  </Flex>

                  {notesOpen && (
                    <Text type="secondary" style={{ display: 'block', margin: '6px 0 0', maxWidth: 980 }}>
                      {shot.note}
                    </Text>
                  )}

                  <div
                    style={{
                      background: token.colorBgContainer,
                      border: `1px solid ${token.colorBorderSecondary}`,
                      borderRadius: token.borderRadiusLG,
                      height: frameHeight * zoom,
                      marginTop: 12,
                      overflow: 'hidden',
                      width: FRAME_WIDTH * zoom,
                    }}
                  >
                    <div
                      style={{
                        height: frameHeight,
                        transform: `scale(${zoom})`,
                        transformOrigin: 'top left',
                        width: FRAME_WIDTH,
                      }}
                    >
                      <iframe
                        // Remounting the frame is the reload: a frame that has been
                        // poked into a strange state is cheaper to rebuild than to
                        // reset from the outside.
                        key={`${shot.index}-${reloads}`}
                        loading="lazy"
                        onLoad={syncTheme}
                        ref={(element) => {
                          frames.current[shot.index] = element;
                        }}
                        src={frameSrc(shot.hash)}
                        style={{ border: 0, display: 'block', height: '100%', width: '100%' }}
                        title={`${shot.title} — ${shot.state}`}
                      />
                    </div>
                  </div>
                </section>
              ))}
            </Flex>
          </div>
        ))}

        <Divider style={{ marginTop: 64 }} />
        <Flex gap={8} align="center">
          <Button href={paths.overview} icon={<ExportOutlined />} type="text">
            Leave the gallery and use the panel
          </Button>
          <Text type="secondary" style={{ fontSize: 12 }}>
            Frames reload lazily as you scroll. In dev each one boots its own copy of the app, so
            this page is heaviest there — it is lightest from the single-file build.
          </Text>
        </Flex>
      </div>
    </div>
  );
}
