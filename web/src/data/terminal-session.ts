/**
 * The React-free half of the Compose terminal: the `ws://` URL builder, the
 * text-frame encode/decode the tab shares with the Go session, and the small
 * state machine that maps server frames and socket closes onto what the tab
 * renders. Nothing here opens a socket or touches React, so every rule is
 * directly testable; the xterm wiring lives in the task's component.
 *
 * Wire protocol (`.scratch/compose-terminal/spec.md`): keystrokes travel as
 * binary frames and are the component's concern; this module builds the
 * `resize` control frame and parses the three server text frames —
 * `ready | exit | error`. Anything else is ignored as `undefined`.
 */

import type { ComposePath } from './api';

export type TerminalShell = 'auto' | 'bash' | 'sh';
export type TerminalUser = 'default' | 'root';

/** What the tab asks the session to connect as. `container` pins one Container of the Service. */
export type TerminalTarget = {
  service: string;
  container?: string;
  shell: TerminalShell;
  user: TerminalUser;
};

export type ServerFrame =
  | { type: 'ready'; shell: 'bash' | 'sh'; user: TerminalUser; container: string }
  | { type: 'exit'; code: number }
  | { type: 'error'; message: string };

export type SessionState =
  | { kind: 'idle' }
  | { kind: 'connecting' }
  | { kind: 'open'; ready: Extract<ServerFrame, { type: 'ready' }> }
  | { kind: 'exited'; code: number; container: string }
  | { kind: 'failed'; message: string }
  | { kind: 'lost'; container?: string };

/**
 * The `ws://`/`wss://` URL for one session: `http(s)` on the page becomes
 * `ws(s)` on the socket, the three Compose ids become the terminal route, and
 * the target and initial size ride the query. The Service name is
 * `encodeURIComponent`-encoded (not form-encoded) so a space stays `%20`.
 */
export function terminalUrl(
  location: ComposePath,
  target: TerminalTarget,
  rows: number,
  cols: number,
  origin: string,
): string {
  const url = new URL(origin);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.pathname = `/api/projects/${location.projectId}/environments/${location.environmentId}/composes/${location.composeId}/terminal`;
  url.search = targetQuery(target, rows, cols);
  return url.toString();
}

function targetQuery(target: TerminalTarget, rows: number, cols: number): string {
  const parts = [`service=${encodeURIComponent(target.service)}`];
  if (target.container !== undefined) {
    parts.push(`container=${encodeURIComponent(target.container)}`);
  }
  parts.push(`shell=${target.shell}`, `user=${target.user}`, `rows=${rows}`, `cols=${cols}`);
  return `?${parts.join('&')}`;
}

/**
 * Parses one server text frame. `ready` must name a real shell and user and a
 * Container; `exit` must carry a numeric code; `error` must carry a message.
 * Any other type, malformed JSON, or a field of the wrong shape is ignored.
 */
export function parseServerFrame(text: string): ServerFrame | undefined {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return undefined;
  }
  if (typeof value !== 'object' || value === null) {
    return undefined;
  }
  const frame = value as Record<string, unknown>;
  switch (frame.type) {
    case 'ready':
      if (
        (frame.shell === 'bash' || frame.shell === 'sh') &&
        (frame.user === 'default' || frame.user === 'root') &&
        typeof frame.container === 'string'
      ) {
        return { type: 'ready', shell: frame.shell, user: frame.user, container: frame.container };
      }
      return undefined;
    case 'exit':
      if (typeof frame.code === 'number' && Number.isFinite(frame.code)) {
        return { type: 'exit', code: frame.code };
      }
      return undefined;
    case 'error':
      if (typeof frame.message === 'string') {
        return { type: 'error', message: frame.message };
      }
      return undefined;
    default:
      return undefined;
  }
}

/** The text `resize` control frame; the server clamps rows/cols to 1–500. */
export function resizeFrame(rows: number, cols: number): string {
  return JSON.stringify({ type: 'resize', rows, cols });
}

/**
 * The session state machine. `ready` opens; `exit` records the code and keeps
 * the Container; `error` fails; a close while connecting or open loses the
 * session (with the Container when one was known); a close after a terminal
 * state changes nothing.
 */
export function nextState(
  state: SessionState,
  event: { type: 'frame'; frame: ServerFrame } | { type: 'close' },
): SessionState {
  if (event.type === 'close') {
    if (state.kind === 'open') {
      return { kind: 'lost', container: state.ready.container };
    }
    if (state.kind === 'connecting') {
      return { kind: 'lost' };
    }
    return state;
  }

  const frame = event.frame;
  if (frame.type === 'ready') {
    return { kind: 'open', ready: frame };
  }
  if (frame.type === 'exit') {
    if (state.kind === 'open') {
      return { kind: 'exited', code: frame.code, container: state.ready.container };
    }
    return state;
  }
  // The only remaining frame is `error`.
  if (state.kind === 'connecting' || state.kind === 'open') {
    return { kind: 'failed', message: frame.message };
  }
  return state;
}

/**
 * What a key press means to the terminal box. Ctrl+C stays SIGINT, so copy is
 * the explicit Ctrl+Shift+C (Cmd+C on a Mac). Paste is Ctrl+Shift+V / Cmd+V;
 * the component leaves the browser's own `paste` event to xterm, so a paste
 * lands exactly once.
 */
export function terminalKeyAction(
  event: Pick<KeyboardEvent, 'type' | 'key' | 'ctrlKey' | 'shiftKey' | 'metaKey'>,
): 'copy' | 'paste' | 'pass' {
  if (event.type !== 'keydown') {
    return 'pass';
  }
  const key = event.key.toLowerCase();
  const chord = (event.ctrlKey && event.shiftKey) || (event.metaKey && !event.ctrlKey);
  if (chord && key === 'c') {
    return 'copy';
  }
  if (chord && key === 'v') {
    return 'paste';
  }
  return 'pass';
}
