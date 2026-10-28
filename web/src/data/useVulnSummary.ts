import useSWR from 'swr';

import { type VulnScope, api, vulnSummaryUrl } from './api';

/**
 * Vulnerability counts for the host, a Project or a Compose. Shared by the
 * menu badge and the badges on Overview, Project and Compose; slow-moving, so
 * it polls once a minute.
 */
export function useVulnSummary(scope: VulnScope, enabled = true) {
  return useSWR(enabled ? vulnSummaryUrl(scope) : null, () => api.vulnSummary(scope), {
    errorRetryCount: 1,
    refreshInterval: 60_000,
  });
}
