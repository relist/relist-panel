// @vitest-environment jsdom
import '../test/dom-setup';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { SWRConfig, useSWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import type { HostMetrics } from './types';
import { useDiskPressure } from './useDiskPressure';

vi.mock('./api', () => ({ api: { hostMetrics: vi.fn() } }));
function payload(used = 900, at = Date.now()): HostMetrics {
  return { window: '15m', from: 'ring', intervalSeconds: 10, partial: true, sources: { diskSpace: { available: true } },
    samples: [{ at, diskUsed: used, diskTotal: 1000 } as never] };
}
function Reading({ enabled = true }: { enabled?: boolean }) {
  const reading = useDiskPressure(enabled);
  return <span>{reading.kind}{'percent' in reading ? ` ${reading.percent}` : ''}</span>;
}
function Refresh() {
  const { mutate } = useSWRConfig();
  return <button onClick={() => { void mutate('/api/host/metrics?window=15m').catch(() => undefined); }}>Refresh</button>;
}
async function tick(ms = 0) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }
beforeEach(() => {
  vi.useFakeTimers(); vi.setSystemTime(100_000);
  // SWR schedules cached revalidation on the next animation frame. Control
  // that browser clock as well as its HTTP retry timers in this jsdom test.
  if (typeof window.requestAnimationFrame === 'function') {
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => window.setTimeout(() => callback(Date.now()), 1));
  }
});
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.resetAllMocks(); });

describe('useDiskPressure', () => {
  it('shares requests and polls the existing 15m metrics every 10 seconds', async () => {
    vi.mocked(api.hostMetrics).mockImplementation(async () => payload());
    render(<SWRConfig value={{ provider: () => new Map() }}><Reading /><Reading /></SWRConfig>);
    await tick();
    expect(screen.getAllByText('warning 90')).toHaveLength(2);
    expect(api.hostMetrics).toHaveBeenCalledExactlyOnceWith('15m');
    await tick(9999);
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
    await tick(1);
    expect(api.hostMetrics).toHaveBeenCalledTimes(2);
  });

  it('skips authentication-disabled reads, revalidates when enabled and stops polling when disabled', async () => {
    vi.mocked(api.hostMetrics).mockImplementation(async () => payload());
    const cache = new Map();
    const content = (enabled: boolean) => <SWRConfig value={{ provider: () => cache }}><Reading enabled={enabled} /></SWRConfig>;
    const result = render(content(false));
    await tick(20_000);
    expect(api.hostMetrics).not.toHaveBeenCalled();
    result.rerender(content(true));
    await tick();
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
    result.rerender(content(false));
    await tick(20_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
    result.rerender(content(true));
    await tick(20);
    expect(api.hostMetrics).toHaveBeenCalledTimes(2);
  });

  it('ages cached readings, backs off failures, then recovers without a stale healthy claim', async () => {
    vi.mocked(api.hostMetrics).mockResolvedValueOnce(payload()).mockRejectedValue(new Error('offline'));
    const result = render(<SWRConfig value={{ provider: () => new Map() }}><Reading /></SWRConfig>);
    await tick();
    await tick(10_000);
    expect(screen.getByText('stale 90')).toBeTruthy();
    expect(api.hostMetrics).toHaveBeenCalledTimes(2);
    await tick(10_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(3);
    await tick(19_999);
    expect(api.hostMetrics).toHaveBeenCalledTimes(3);
    vi.mocked(api.hostMetrics).mockImplementation(async () => payload(700));
    await tick(1);
    expect(screen.getByText('healthy 70')).toBeTruthy();
    expect(api.hostMetrics).toHaveBeenCalledTimes(4);
    result.unmount();
    await tick(120_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(4);
  });

  it('marks an unchanged sample stale even when requests keep succeeding', async () => {
    vi.mocked(api.hostMetrics).mockResolvedValue(payload(700));
    render(<SWRConfig value={{ provider: () => new Map() }}><Reading /></SWRConfig>);
    await tick();
    await tick(40_000);
    expect(screen.getByText('stale 70')).toBeTruthy();
  });

  it('cleans up pending retries on disable and after unmount', async () => {
    vi.mocked(api.hostMetrics).mockRejectedValue(new Error('offline'));
    const cache = new Map();
    const content = (enabled: boolean) => <SWRConfig value={{ provider: () => cache }}><Reading enabled={enabled} /></SWRConfig>;
    const result = render(content(true));
    await tick();
    expect(screen.getByText('unavailable')).toBeTruthy();
    result.rerender(content(false));
    await tick(120_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
    result.unmount();
    await tick(120_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
  });

  it('caps repeated failure backoff at one read per minute', async () => {
    vi.mocked(api.hostMetrics).mockRejectedValue(new Error('offline'));
    render(<SWRConfig value={{ provider: () => new Map() }}><Reading /></SWRConfig>);
    await tick();
    await tick(10_000);
    await tick(20_000);
    await tick(40_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(4);
    await tick(59_999);
    expect(api.hostMetrics).toHaveBeenCalledTimes(4);
    await tick(1);
    expect(api.hostMetrics).toHaveBeenCalledTimes(5);
    await tick(60_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(6);
  });

  it('does not retry a read after its session is denied', async () => {
    vi.mocked(api.hostMetrics).mockRejectedValue(Object.assign(new Error('Sign in again'), { status: 401 }));
    render(<SWRConfig value={{ provider: () => new Map() }}><Reading /></SWRConfig>);
    await tick();
    expect(screen.getByText('unavailable')).toBeTruthy();
    await tick(120_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
  });

  it('does not schedule a retry when a pending request rejects after unmount', async () => {
    let rejectRead!: (reason: Error) => void;
    vi.mocked(api.hostMetrics).mockReturnValue(new Promise((_resolve, reject) => { rejectRead = reject; }));
    const result = render(<SWRConfig value={{ provider: () => new Map() }}><Reading /></SWRConfig>);
    await tick();
    result.unmount();
    await tick(30_000);
    const timers = vi.getTimerCount();
    await act(async () => { rejectRead(new Error('late failure')); });
    expect(vi.getTimerCount()).toBe(timers);
    await tick(120_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
  });

  it('does not schedule a retry when a pending request rejects after authentication is disabled', async () => {
    let rejectRead!: (reason: Error) => void;
    vi.mocked(api.hostMetrics).mockReturnValue(new Promise((_resolve, reject) => { rejectRead = reject; }));
    const cache = new Map();
    const content = (enabled: boolean) => <SWRConfig value={{ provider: () => cache }}><Reading enabled={enabled} /></SWRConfig>;
    const result = render(content(true));
    await tick();
    result.rerender(content(false));
    await tick(30_000);
    const timers = vi.getTimerCount();
    await act(async () => { rejectRead(new Error('late failure')); });
    expect(vi.getTimerCount()).toBe(timers);
    await tick(120_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(1);
  });

  it('cancels an older network retry when revalidation reports an expired session', async () => {
    vi.mocked(api.hostMetrics).mockRejectedValueOnce(new Error('offline'))
      .mockRejectedValue(Object.assign(new Error('Sign in again'), { status: 401 }));
    render(<SWRConfig value={{ provider: () => new Map() }}><Reading /><Refresh /></SWRConfig>);
    await tick();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await tick();
    expect(api.hostMetrics).toHaveBeenCalledTimes(2);
    await tick(120_000);
    expect(api.hostMetrics).toHaveBeenCalledTimes(2);
  });
});
