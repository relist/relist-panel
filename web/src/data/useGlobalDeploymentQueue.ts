import useSWR from 'swr';
import { api } from './api';

/** One host queue shared by the menu badge and Deployments page. */
export function useGlobalDeploymentQueue(enabled = true) {
  return useSWR(enabled ? '/api/deployments/queue' : null, () => api.globalDeploymentQueue(), {
    dedupingInterval: 500,
    errorRetryInterval: 5000,
    errorRetryCount: 3,
    refreshInterval: (data) => data?.length ? 1000 : 5000,
  });
}
