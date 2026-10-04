import { describe, expect, it } from 'vitest';

import type { ComposePath } from './api';
import {
  nextState,
  parseServerFrame,
  resizeFrame,
  terminalUrl,
  type ServerFrame,
  type SessionState,
} from './terminal-session';

/**
 * The React-free half of the Terminal tab: the ws:// URL the browser dials, the
 * server's text frames, the resize frame it sends back, and the small state
 * machine that turns frames and socket closes into what the tab renders. Nothing
 * here touches a socket or React, so every rule is asserted directly.
 */

const location: ComposePath = { projectId: 1, environmentId: 2, composeId: 3 };

describe('terminalUrl', () => {
  it('upgrades https to wss and carries the target and size as the query', () => {
    expect(
      terminalUrl(location, { service: 'app', shell: 'auto', user: 'default' }, 24, 80, 'https://panel.example'),
    ).toBe(
      'wss://panel.example/api/projects/1/environments/2/composes/3/terminal?service=app&shell=auto&user=default&rows=24&cols=80',
    );
  });

  it('upgrades http to ws', () => {
    expect(
      terminalUrl(location, { service: 'app', shell: 'bash', user: 'root' }, 40, 120, 'http://panel.example'),
    ).toBe(
      'ws://panel.example/api/projects/1/environments/2/composes/3/terminal?service=app&shell=bash&user=root&rows=40&cols=120',
    );
  });

  it('adds container only when one is named', () => {
    expect(
      terminalUrl(
        location,
        { service: 'app', container: 'relist-c3-app-1', shell: 'sh', user: 'default' },
        24,
        80,
        'https://panel.example',
      ),
    ).toBe(
      'wss://panel.example/api/projects/1/environments/2/composes/3/terminal?service=app&container=relist-c3-app-1&shell=sh&user=default&rows=24&cols=80',
    );
  });

  it('encodes a Service name with a space', () => {
    expect(
      terminalUrl(location, { service: 'legacy app', shell: 'auto', user: 'default' }, 24, 80, 'https://panel.example'),
    ).toContain('service=legacy%20app');
  });
});

describe('parseServerFrame', () => {
  it('parses an exit frame with its numeric code', () => {
    expect(parseServerFrame('{"type":"exit","code":3}')).toEqual({ type: 'exit', code: 3 });
  });

  it('parses a ready frame with the resolved target', () => {
    expect(
      parseServerFrame('{"type":"ready","shell":"bash","user":"root","container":"relist-c3-app-1"}'),
    ).toEqual({ type: 'ready', shell: 'bash', user: 'root', container: 'relist-c3-app-1' });
  });

  it('parses an error frame with its message', () => {
    expect(parseServerFrame('{"type":"error","message":"no running container"}')).toEqual({
      type: 'error',
      message: 'no running container',
    });
  });

  it('rejects an unknown frame type', () => {
    expect(parseServerFrame('{"type":"resize","rows":1,"cols":1}')).toBeUndefined();
  });

  it('rejects invalid JSON', () => {
    expect(parseServerFrame('not json')).toBeUndefined();
  });

  it('rejects an exit frame without a numeric code', () => {
    expect(parseServerFrame('{"type":"exit"}')).toBeUndefined();
    expect(parseServerFrame('{"type":"exit","code":"3"}')).toBeUndefined();
  });
});

describe('resizeFrame', () => {
  it('serializes the exact text frame the server reads', () => {
    expect(resizeFrame(40, 120)).toBe('{"type":"resize","rows":40,"cols":120}');
  });
});

describe('nextState', () => {
  const ready: ServerFrame = { type: 'ready', shell: 'bash', user: 'default', container: 'relist-c3-app-1' };

  it('opens on a ready frame', () => {
    expect(nextState({ kind: 'connecting' }, { type: 'frame', frame: ready })).toEqual({
      kind: 'open',
      ready,
    });
  });

  it('records the exit code and keeps the container on exit', () => {
    const state: SessionState = { kind: 'open', ready };
    expect(nextState(state, { type: 'frame', frame: { type: 'exit', code: 3 } })).toEqual({
      kind: 'exited',
      code: 3,
      container: 'relist-c3-app-1',
    });
  });

  it('fails on an error frame while connecting', () => {
    expect(
      nextState({ kind: 'connecting' }, { type: 'frame', frame: { type: 'error', message: 'boom' } }),
    ).toEqual({ kind: 'failed', message: 'boom' });
  });

  it('drops a lost connection to lost and keeps the container', () => {
    const state: SessionState = { kind: 'open', ready };
    expect(nextState(state, { type: 'close' })).toEqual({ kind: 'lost', container: 'relist-c3-app-1' });
  });

  it('reports a close before ready as lost, with no container yet', () => {
    expect(nextState({ kind: 'connecting' }, { type: 'close' })).toEqual({ kind: 'lost' });
  });

  it('leaves an exited session exited when the socket closes', () => {
    const state: SessionState = { kind: 'exited', code: 0, container: 'relist-c3-app-1' };
    expect(nextState(state, { type: 'close' })).toEqual(state);
  });

  it('leaves a failed session failed when the socket closes', () => {
    const state: SessionState = { kind: 'failed', message: 'boom' };
    expect(nextState(state, { type: 'close' })).toEqual(state);
  });
});
