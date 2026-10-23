import { api } from '../data/api';
import type { Compose, DeploymentRequest } from '../data/types';

/**
 * Queues a Deployment from the Project's Compose list. A Compose with a
 * Source must build (`deploy` mode would run stale code), so the Source is
 * looked up first; if that fails nothing is queued. Returns the number.
 */
export async function deployFromList(
  compose: Compose,
  projectId: number,
  enqueue: (composeId: number, req: DeploymentRequest) => Promise<number>,
): Promise<number> {
  const source = await api.getSource({ composeId: compose.id, environmentId: compose.environmentId, projectId });
  return enqueue(compose.id, { mode: source ? 'build' : 'deploy' });
}
