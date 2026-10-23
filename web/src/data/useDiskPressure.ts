import { useEffect, useRef, useState } from 'react';
import useSWR from 'swr';
import { api } from './api';
import { diskPressure, DISK_POLL_MS } from './disk-pressure';

/** The sidebar's authenticated disk read uses the existing sampler endpoint. */
export function useDiskPressure(enabled: boolean) {
  const retryTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const mounted = useRef(true);
  const enabledNow = useRef(enabled);
  const [now, setNow] = useState(Date.now);
  const { data, error } = useSWR(enabled ? '/api/host/metrics?window=15m' : null, () => api.hostMetrics('15m'), {
    dedupingInterval: 1000,
    refreshInterval: DISK_POLL_MS,
    revalidateOnMount: true,
    onSuccess: () => clearTimeout(retryTimer.current),
    onErrorRetry: (reason, _key, _config, revalidate, { retryCount }) => {
      clearTimeout(retryTimer.current);
      const status = (reason as { status?: number } | null)?.status;
      if (!mounted.current || !enabledNow.current || status === 401 || status === 403) return;
      // SWR skips its normal poll while an error is present. Retry that read
      // at 10s, 20s, 40s, then at most once per minute until recovery.
      const delay = Math.min(60_000, DISK_POLL_MS * 2 ** Math.min(Math.max(retryCount - 1, 0), 3));
      retryTimer.current = setTimeout(() => {
        if (mounted.current && enabledNow.current) void revalidate({ retryCount });
      }, delay);
    },
  });
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; clearTimeout(retryTimer.current); };
  }, []);
  useEffect(() => {
    enabledNow.current = enabled;
    if (!enabled) { clearTimeout(retryTimer.current); return; }
    setNow(Date.now());
    // Age a cached sample even when a request stalls or retries back off.
    const clock = setInterval(() => setNow(Date.now()), DISK_POLL_MS);
    return () => { clearInterval(clock); clearTimeout(retryTimer.current); };
  }, [enabled]);
  return diskPressure(enabled ? data : undefined, enabled ? error : undefined, now);
}
