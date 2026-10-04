import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import type { ITheme } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { Alert, Button, Card, Flex, Modal, Segmented, Select, Skeleton, Typography, theme } from 'antd';
import type { GlobalToken } from 'antd';
import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import { useBeforeUnload, useBlocker, useNavigate } from 'react-router';
import type { BlockerFunction } from 'react-router';

import { paths, resolvePath } from '../app/paths';
import { LAYOUT, ThemeModeContext } from '../app/theme';
import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import { nextState, parseServerFrame, resizeFrame, terminalKeyAction, terminalUrl } from '../data/terminal-session';
import type { SessionState, TerminalShell, TerminalTarget, TerminalUser } from '../data/terminal-session';
import type { Compose, TerminalServices } from '../data/types';
import { EmptyState } from './primitives';

const { Text } = Typography;

/**
 * The Compose screen's Terminal tab (GUIDELINES §4.6, `.scratch/compose-terminal`):
 * one interactive shell into a running Container of a chosen Service. The
 * protocol half — the `ws://` URL, the `ready | exit | error` frames and the
 * session state machine — lives React-free in `data/terminal-session.ts`; this
 * file owns xterm, the socket, the toolbars and the leave guard.
 *
 * The tab mounts lazily on its first visit and stays mounted while the Compose
 * page is open (`Tabs` has no `destroyOnHidden`), so switching Editor ↔ Terminal
 * keeps the session. Leaving the page unmounts this component, which closes the
 * socket with 1000 — the backend then hangs up the shell.
 */

type ServiceEntry = TerminalServices['services'][number];
type ContainerEntry = ServiceEntry['containers'][number];

/** The selectors' remembered picks. Names/ids, never indexes: a deploy rebuilds the list. */
type Picks = {
  container?: string;
  service?: string;
  shell: TerminalShell;
  user: TerminalUser;
};

/** The picks resolved against one `terminal/services` answer. */
type Selection = {
  container?: ContainerEntry;
  containers: ContainerEntry[];
  service?: ServiceEntry;
  shell: TerminalShell;
  user: TerminalUser;
};

/**
 * xterm's colours come from the design tokens, the way `monacoThemeData` maps
 * them for Monaco: surfaces and text from the same ramp as the rest of the
 * panel. The font is a terminal *option*, not a theme key, so it is set beside
 * this (see the theme effect in the component).
 */
function xtermTheme(token: GlobalToken): ITheme {
  return {
    background: token.colorBgContainer,
    cursor: token.colorPrimary,
    cursorAccent: token.colorBgContainer,
    foreground: token.colorText,
    selectionBackground: token.colorPrimaryBg,
  };
}

/** A shell the probe did not find falls back to a usable one, then to `auto`. */
function chooseShell(shell: TerminalShell, shells: { bash: boolean; sh: boolean }): TerminalShell {
  if (shell === 'bash' && !shells.bash) {
    return shells.sh ? 'sh' : 'auto';
  }
  if (shell === 'sh' && !shells.sh) {
    return shells.bash ? 'bash' : 'auto';
  }
  return shell;
}

/**
 * Resolves the remembered picks against one listing. The Service is the first
 * one with a running Container unless the pick is still running; the Container
 * is the lowest-numbered unless the picked id is still there. So a redeploy's
 * new Container is picked up without the operator touching the selectors.
 */
function resolveSelection(payload: TerminalServices | undefined, picks: Picks): Selection {
  const running = (payload?.services ?? []).filter((entry) => entry.containers.length > 0);
  const service = running.find((entry) => entry.name === picks.service) ?? running[0];
  const containers = service ? [...service.containers].sort((a, b) => a.number - b.number) : [];
  const container = containers.find((entry) => entry.id === picks.container) ?? containers[0];
  const shell = container ? chooseShell(picks.shell, container.shells) : 'auto';
  return { container, containers, service, shell, user: picks.user };
}

function userLabel(user: TerminalUser): string {
  return user === 'root' ? 'root' : 'image default';
}

export function ComposeTerminalTab({
  active,
  compose,
  location,
  onOpenEditor,
}: {
  /** The Terminal tab is the one on screen; a hidden pane measures zero width. */
  active: boolean;
  compose: Compose;
  /** The three ids the terminal route is built from. */
  location: ComposePath;
  /** Sends the operator to the Editor tab, where deploying lives. */
  onOpenEditor: () => void;
}) {
  const navigate = useNavigate();
  const mode = useContext(ThemeModeContext);
  const { token } = theme.useToken();

  const [services, setServices] = useState<TerminalServices>();
  const [servicesError, setServicesError] = useState<string>();
  const [servicesLoading, setServicesLoading] = useState(false);
  const [pickedService, setPickedService] = useState<string>();
  const [pickedContainer, setPickedContainer] = useState<string>();
  const [pickedShell, setPickedShell] = useState<TerminalShell>('auto');
  const [pickedUser, setPickedUser] = useState<TerminalUser>('default');
  const [session, setSession] = useState<SessionState>({ kind: 'idle' });
  const [expired, setExpired] = useState(false);
  // Once the box exists it stays until the page unmounts, so a Reconnect that
  // finds nothing running (a redeploy in progress) cannot wipe the output.
  const [boxCreated, setBoxCreated] = useState(false);

  // The socket handlers close over state, so the machine lives in a ref beside
  // React state; `applyState` keeps the two in step.
  const sessionRef = useRef<SessionState>({ kind: 'idle' });
  const wsRef = useRef<WebSocket | undefined>(undefined);
  const termRef = useRef<Terminal | undefined>(undefined);
  const fitRef = useRef<FitAddon | undefined>(undefined);
  const containerRef = useRef<HTMLDivElement>(null);
  const resizeTimer = useRef<number | undefined>(undefined);
  const pendingSize = useRef<{ cols: number; rows: number } | undefined>(undefined);
  const rafRef = useRef<number | undefined>(undefined);
  const loadedCompose = useRef<number | undefined>(undefined);

  const selection = resolveSelection(services, {
    container: pickedContainer,
    service: pickedService,
    shell: pickedShell,
    user: pickedUser,
  });
  const shells = selection.container?.shells ?? { bash: false, sh: false };
  // The toolbar and the box exist only once there is something to shell into.
  const running = services?.available === true && selection.service !== undefined;
  const busy = session.kind === 'connecting' || session.kind === 'open';
  const showBox = running || boxCreated;
  // The toolbar stays in place over a kept box, but has nothing to act on.
  const locked = busy || !running;

  const applyState = (next: SessionState) => {
    sessionRef.current = next;
    setSession(next);
  };

  // ---- Services listing ------------------------------------------------------

  const loadServices = useCallback(async (): Promise<TerminalServices | undefined> => {
    setServicesLoading(true);
    setServicesError(undefined);
    try {
      const payload = await api.terminalServices(location);
      setServices(payload);
      return payload;
    } catch (reason: unknown) {
      setServicesError(reason instanceof Error ? reason.message : 'Could not load this Compose’s Services');
      return undefined;
    } finally {
      setServicesLoading(false);
    }
  }, [location.composeId, location.environmentId, location.projectId]);

  // Load once, the first time the tab is on screen — and again on every
  // Reconnect/Retry, so a redeploy's new Container is picked up.
  useEffect(() => {
    if (!active || loadedCompose.current === location.composeId) {
      return;
    }
    loadedCompose.current = location.composeId;
    void loadServices();
  }, [active, loadServices, location.composeId]);

  // ---- xterm -----------------------------------------------------------------

  const queueResize = (rows: number, cols: number) => {
    pendingSize.current = { cols, rows };
    if (resizeTimer.current !== undefined) {
      window.clearTimeout(resizeTimer.current);
    }
    resizeTimer.current = window.setTimeout(() => {
      const socket = wsRef.current;
      const size = pendingSize.current;
      if (socket && socket.readyState === WebSocket.OPEN && size) {
        socket.send(resizeFrame(size.rows, size.cols));
      }
    }, 100);
  };

  const scheduleFit = () => {
    if (rafRef.current !== undefined) {
      cancelAnimationFrame(rafRef.current);
    }
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = undefined;
      const fit = fitRef.current;
      const el = containerRef.current;
      // A hidden pane measures zero; fitting it would only warn.
      if (!fit || !el || el.clientWidth === 0 || el.clientHeight === 0) {
        return;
      }
      fit.fit();
    });
  };

  useEffect(() => {
    const el = containerRef.current;
    if (!el || termRef.current) {
      return undefined;
    }
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: token.fontFamilyCode,
      fontSize: 13,
      scrollback: 5000,
      theme: xtermTheme(token),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    termRef.current = term;
    fitRef.current = fit;
    setBoxCreated(true);

    term.onData((data) => {
      const socket = wsRef.current;
      if (socket && socket.readyState === WebSocket.OPEN) {
        socket.send(new TextEncoder().encode(data));
      }
    });
    term.onResize(({ rows, cols }) => queueResize(rows, cols));

    // Ctrl+C stays SIGINT: copy needs the explicit Ctrl+Shift+C (Cmd+C on a
    // Mac) and only steals the key when there is a selection. Paste returns
    // false so xterm ignores the chord and the browser's native `paste` event
    // reaches it once, with no clipboard permission needed.
    term.attachCustomKeyEventHandler((event) => {
      switch (terminalKeyAction(event)) {
        case 'copy': {
          const selected = term.getSelection();
          if (!selected) {
            return true;
          }
          void navigator.clipboard?.writeText(selected).catch(() => undefined);
          return false;
        }
        case 'paste':
          return false;
        default:
          return true;
      }
    });

    const observer = new ResizeObserver(() => scheduleFit());
    observer.observe(el);
    scheduleFit();

    return () => {
      observer.disconnect();
      if (rafRef.current !== undefined) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = undefined;
      }
      term.dispose();
      termRef.current = undefined;
      fitRef.current = undefined;
    };
    // Created when the box first appears and kept until unmount, never keyed on
    // `running` alone; theme changes are applied in place below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [boxCreated || running]);

  // A hidden pane measured zero on mount; refit once it is the active tab.
  useEffect(() => {
    if (active) {
      scheduleFit();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  // antd tokens move with the theme; re-apply rather than recreate, so output
  // and scrollback survive.
  useEffect(() => {
    const term = termRef.current;
    if (!term) {
      return;
    }
    term.options.theme = xtermTheme(token);
    term.options.fontFamily = token.fontFamilyCode;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, token.colorBgContainer, token.colorPrimary, token.colorPrimaryBg, token.colorText, token.fontFamilyCode]);

  // ---- Session lifecycle -----------------------------------------------------

  const closeSocket = () => {
    const socket = wsRef.current;
    wsRef.current = undefined;
    if (socket && (socket.readyState === WebSocket.CONNECTING || socket.readyState === WebSocket.OPEN)) {
      socket.close(1000);
    }
  };

  // Unmount closes the shell: the backend hangs the process up on the stream.
  useEffect(
    () => () => {
      if (resizeTimer.current !== undefined) {
        window.clearTimeout(resizeTimer.current);
      }
      closeSocket();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const startSession = (target: TerminalTarget) => {
    closeSocket();
    setExpired(false);
    const term = termRef.current;
    const socket = new WebSocket(
      terminalUrl(location, target, term?.rows ?? 24, term?.cols ?? 80, window.location.origin),
    );
    socket.binaryType = 'arraybuffer';
    wsRef.current = socket;
    applyState({ kind: 'connecting' });

    socket.onmessage = (event) => {
      if (typeof event.data !== 'string') {
        if (event.data instanceof ArrayBuffer) {
          termRef.current?.write(new Uint8Array(event.data));
        }
        return;
      }
      const frame = parseServerFrame(event.data);
      if (!frame) {
        return;
      }
      applyState(nextState(sessionRef.current, { type: 'frame', frame }));
      if (frame.type === 'exit') {
        termRef.current?.write(`\r\n\x1b[2m[exited with code ${frame.code}]\x1b[0m\r\n`);
      }
    };

    socket.onclose = () => {
      if (wsRef.current !== socket) {
        // An intentional Disconnect or a replacement already moved on.
        return;
      }
      wsRef.current = undefined;
      const previous = sessionRef.current;
      const next = nextState(previous, { type: 'close' });
      applyState(next);
      if (next.kind === 'lost') {
        termRef.current?.write('\r\n\x1b[2m── connection lost ──\x1b[0m\r\n');
      }
      if (previous.kind === 'connecting') {
        // Closed before `ready`: tell an expired session from a lost one.
        void api
          .authStatus()
          .then((status) => {
            if (!status.authenticated) {
              setExpired(true);
            }
          })
          .catch(() => undefined);
      }
    };
  };

  const disconnect = () => {
    closeSocket();
    setExpired(false);
    applyState({ kind: 'idle' });
  };

  const connectWith = (payload: TerminalServices | undefined) => {
    const next = resolveSelection(payload, {
      container: pickedContainer,
      service: pickedService,
      shell: pickedShell,
      user: pickedUser,
    });
    if (!next.service || !next.container) {
      return;
    }
    startSession({
      container: next.container.id,
      service: next.service.name,
      shell: next.shell,
      user: next.user,
    });
  };

  // Connect, Retry and Reconnect are one path: re-read the listing, then open
  // a new shell. Reconnect never happens on its own.
  const openShell = async () => {
    const fresh = await loadServices();
    connectWith(fresh ?? services);
  };

  // ---- Leave guard -----------------------------------------------------------

  const shouldBlock = useCallback<BlockerFunction>(
    ({ currentLocation, nextLocation }) => {
      if (sessionRef.current.kind !== 'open') {
        return false;
      }
      const next = resolvePath(nextLocation.pathname);
      if (!next) {
        return true;
      }
      const current = resolvePath(currentLocation.pathname);
      return next.pattern !== current?.pattern || next.params.composeId !== String(location.composeId);
    },
    [location.composeId],
  );
  const blocker = useBlocker(shouldBlock);
  const confirmUnload = useCallback((event: BeforeUnloadEvent) => {
    if (sessionRef.current.kind !== 'open') {
      return;
    }
    event.preventDefault();
    event.returnValue = '';
  }, []);
  useBeforeUnload(confirmUnload);

  // ---- Render ----------------------------------------------------------------

  const noneRunningCopy = `A terminal opens a shell in a running Container. ${compose.name} has none on the host yet, and deploying is what starts them.`
  const statusText =
    session.kind === 'open'
      ? `Connected · ${session.ready.shell} · ${userLabel(session.ready.user)} · ${session.ready.container}`
      : undefined;
  const confirmContainer =
    session.kind === 'open' ? session.ready.container : (selection.container?.name ?? 'this container');

  return (
    <>
      {servicesError && (
        <Alert
          action={<Button onClick={() => void loadServices()}>Retry</Button>}
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          title={servicesError}
          type="error"
        />
      )}

      {services === undefined ? (
        servicesLoading ? (
          <Skeleton active paragraph={{ rows: 6 }} title={false} />
        ) : null
      ) : !showBox && !services.available ? (
        <Alert
          action={<Button onClick={() => void loadServices()}>Retry</Button>}
          description={services.reason}
          showIcon
          title="Terminal is unavailable"
          type="warning"
        />
      ) : !showBox ? (
        <Card variant="outlined">
          <EmptyState
            action={
              <Button onClick={onOpenEditor} type="primary">
                Open the Editor tab
              </Button>
            }
            description={noneRunningCopy}
            title="Deploy to start a terminal"
          />
        </Card>
      ) : (
        <>
          <Flex align="center" gap={12} style={{ marginBottom: LAYOUT.gutterSmall }} wrap>
            <Select
              aria-label="Service"
              disabled={locked}
              onChange={(value) => {
                setPickedService(value);
                setPickedContainer(undefined);
              }}
              options={services.services.map((entry) => ({
                disabled: entry.containers.length === 0,
                label: entry.containers.length === 0 ? `${entry.name} — not running` : entry.name,
                value: entry.name,
              }))}
              style={{ minWidth: 200 }}
              value={selection.service?.name}
            />
            {selection.containers.length > 1 && (
              <Select
                aria-label="Container"
                disabled={locked}
                onChange={setPickedContainer}
                options={selection.containers.map((entry) => ({ label: entry.name, value: entry.id }))}
                style={{ minWidth: 220 }}
                value={selection.container?.id}
              />
            )}
            <Flex align="center" gap={8}>
              <Text type="secondary">Shell</Text>
              <Segmented
                disabled={locked}
                onChange={(value) => setPickedShell(value as TerminalShell)}
                options={[
                  { label: 'Auto', value: 'auto' },
                  {
                    disabled: !shells.bash,
                    label: 'bash',
                    tooltip: shells.bash ? undefined : 'bash is not installed in this image',
                    value: 'bash',
                  },
                  {
                    disabled: !shells.sh,
                    label: 'sh',
                    tooltip: shells.sh ? undefined : 'sh is not installed in this image',
                    value: 'sh',
                  },
                ]}
                value={selection.shell}
              />
            </Flex>
            <Flex align="center" gap={8}>
              <Text type="secondary">User</Text>
              <Segmented
                disabled={locked}
                onChange={(value) => setPickedUser(value as TerminalUser)}
                options={[
                  { label: 'Image default', value: 'default' },
                  { label: 'root', value: 'root' },
                ]}
                value={selection.user}
              />
            </Flex>
            {session.kind === 'open' ? (
              <Button onClick={disconnect}>Disconnect</Button>
            ) : (
              <Button
                disabled={!running}
                loading={servicesLoading || session.kind === 'connecting'}
                onClick={() => void openShell()}
                type="primary"
              >
                Connect
              </Button>
            )}
            {statusText && (
              <Text style={{ marginInlineStart: 'auto' }} type="secondary">
                {statusText}
              </Text>
            )}
          </Flex>

          {!running && (
            <Alert
              action={
                services.available ? (
                  <Button onClick={onOpenEditor}>Open the Editor tab</Button>
                ) : (
                  <Button onClick={() => void loadServices()}>Retry</Button>
                )
              }
              description={services.available ? noneRunningCopy : services.reason}
              showIcon
              style={{ marginBottom: LAYOUT.gutterSmall }}
              title={services.available ? 'Deploy to start a terminal' : 'Terminal is unavailable'}
              type={services.available ? 'info' : 'warning'}
            />
          )}

          {session.kind === 'failed' && (
            <Alert
              action={<Button onClick={() => void openShell()}>Retry</Button>}
              description="Retry re-reads the Service list and opens a new shell."
              showIcon
              style={{ marginBottom: LAYOUT.gutterSmall }}
              title={session.message}
              type="error"
            />
          )}

          {session.kind === 'exited' && (
            <Flex align="center" gap={8} style={{ marginBottom: LAYOUT.gutterSmall }}>
              <Text type="secondary">The shell exited. Reconnect opens a new shell.</Text>
              <Button onClick={() => void openShell()}>Reconnect</Button>
            </Flex>
          )}

          {session.kind === 'lost' &&
            (expired ? (
              <Alert
                action={
                  <Button onClick={() => navigate(paths.signIn)} type="primary">
                    Sign in
                  </Button>
                }
                showIcon
                style={{ marginBottom: LAYOUT.gutterSmall }}
                title="Your session expired — sign in again"
                type="error"
              />
            ) : (
              <Alert
                action={<Button onClick={() => void openShell()}>Reconnect</Button>}
                description="Reconnect opens a new shell."
                showIcon
                style={{ marginBottom: LAYOUT.gutterSmall }}
                title={`Connection lost — the shell in ${confirmContainer} was closed`}
                type="warning"
              />
            ))}

          <div
            style={{
              background: token.colorBgContainer,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: token.borderRadius,
              height: LAYOUT.terminalHeight,
              overflow: 'hidden',
              padding: 8,
            }}
          >
            <div ref={containerRef} style={{ height: '100%', width: '100%' }} />
          </div>
        </>
      )}

      <Modal
        okText="Leave"
        onCancel={() => blocker.reset?.()}
        onOk={() => blocker.proceed?.()}
        open={blocker.state === 'blocked'}
        title="Leave this Compose?"
      >
        <Text>
          Leaving closes the terminal session in <Text code>{confirmContainer}</Text>.
        </Text>
      </Modal>
    </>
  );
}
