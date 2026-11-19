// @vitest-environment jsdom
import '../test/dom-setup';
import { act, cleanup, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { BOOT_CAP_MS } from './boot-readiness';
import { BOOT_FADE_MS, BootGate, useBootHold } from './BootGate';

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function Hold({ waitingOn }: { waitingOn: string | null }) {
  useBootHold(waitingOn);
  return null;
}

const splash = () => screen.queryByRole('status', { name: 'Loading' });

/** Fade the splash out and let it unmount. */
const fadeOut = () => act(() => vi.advanceTimersByTimeAsync(BOOT_FADE_MS));

describe('BootGate', () => {
  it('keeps the splash up until every hold is released, then fades it away', async () => {
    const { rerender } = render(<BootGate lockSettled><Hold waitingOn="Loading projects" /><p>the app</p></BootGate>);
    expect(splash()).toBeTruthy();
    expect(screen.getByText('Loading projects')).toBeTruthy();
    // The app is mounted underneath so its own loads can run.
    expect(screen.getByText('the app')).toBeTruthy();

    rerender(<BootGate lockSettled><Hold waitingOn={null} /><p>the app</p></BootGate>);
    expect(splash()).toBeTruthy();
    await fadeOut();
    expect(splash()).toBeNull();
  });

  it('waits for the Migration lock read', async () => {
    const { rerender } = render(<BootGate lockSettled={false}><p>the app</p></BootGate>);
    await fadeOut();
    expect(screen.getByText('Checking sign-in')).toBeTruthy();
    rerender(<BootGate lockSettled><p>the app</p></BootGate>);
    await fadeOut();
    expect(splash()).toBeNull();
  });

  it('does not slip through when one hold hands over to the next in the same commit', async () => {
    // The shell releases the tree hold in the same render that hands the
    // Overview its Composes, whose Service reads only then begin.
    function Shell() {
      const [loaded, setLoaded] = useState(false);
      return (
        <>
          <button onClick={() => setLoaded(true)} type="button">load</button>
          <Hold waitingOn={loaded ? null : 'Loading projects'} />
          <Hold waitingOn={loaded ? 'Checking services' : null} />
        </>
      );
    }
    render(<BootGate lockSettled><Shell /></BootGate>);
    act(() => { screen.getByRole('button', { name: 'load' }).click(); });
    await fadeOut();
    expect(splash()).toBeTruthy();
    expect(screen.getByText('Checking services')).toBeTruthy();
  });

  it('never comes back once the panel has shown', async () => {
    const { rerender } = render(<BootGate lockSettled><Hold waitingOn={null} /></BootGate>);
    await fadeOut();
    expect(splash()).toBeNull();
    // A screen reloading the tree around a mutation holds again.
    rerender(<BootGate lockSettled><Hold waitingOn="Loading projects" /></BootGate>);
    await fadeOut();
    expect(splash()).toBeNull();
  });

  it('shows the panel as it is once the cap runs out', async () => {
    render(<BootGate lockSettled><Hold waitingOn="Loading projects" /></BootGate>);
    await act(() => vi.advanceTimersByTimeAsync(BOOT_CAP_MS - 1));
    expect(splash()).toBeTruthy();
    await act(() => vi.advanceTimersByTimeAsync(1));
    await fadeOut();
    expect(splash()).toBeNull();
  });

  it('lets a hold outside the gate do nothing', () => {
    render(<Hold waitingOn="Loading projects" />);
    expect(splash()).toBeNull();
  });
});
