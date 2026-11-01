import useSWR from 'swr';
import { api } from './api';
import type { PanelUpdateStatus } from './panel-update';

/** How often the shell re-reads the update status; the server checks daily, so this is only to notice it. */
const SHELL_POLL_MS = 10 * 60 * 1000;

/**
 * The update status the header reads. It shares the page's SWR key, so a check
 * made on the page moves the header's tag at once. A failed read leaves no tag.
 */
export function usePanelUpdateStatus(enabled: boolean) {
  const { data } = useSWR<PanelUpdateStatus>(enabled ? '/api/panel-update/status' : null, api.panelUpdateStatus, {
    refreshInterval: SHELL_POLL_MS,
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  });
  return data;
}
