import type { Deployment } from './types';

/** The Deployment the host is working on now, as the queue reports it. */
export type ActiveDeployment = { composeName: string; number: number; step: string };

/** A step id as a label: `fetch` → "Fetch", `build:api` → "Build api". */
export function stepLabel(step: string): string {
  const [name, service] = step.split(':', 2);
  const label = name.charAt(0).toUpperCase() + name.slice(1);
  return service ? `${label} ${service}` : label;
}

function activeStepWord(step: string): string {
  if (step.startsWith('build:')) {
    return 'building';
  }
  if (step.startsWith('rollout:')) {
    return 'rolling out';
  }
  return stepLabel(step).toLowerCase();
}

/** The one line a Deployment shows for itself: where it waits, what it does, or how it ended. */
export function deploymentHeadline(d: Deployment, active?: ActiveDeployment): string {
  switch (d.status) {
    case 'queued': {
      const ahead = d.ahead === undefined ? 'Waiting' : `Waiting — ${d.ahead} ahead`;
      return active ? `${ahead} (${active.composeName} #${active.number} ${activeStepWord(active.step)})` : ahead;
    }
    case 'running':
      if (d.step.startsWith('build:')) {
        return `Building ${d.step.slice('build:'.length)}`;
      }
      if (d.step.startsWith('rollout:')) {
        return `Rolling out ${d.step.slice('rollout:'.length)}`;
      }
      return d.step ? stepLabel(d.step) : 'Starting';
    case 'failed':
      return d.failedStep ? `Failed at ${stepLabel(d.failedStep)}` : 'Failed';
    case 'canceled':
      return 'Canceled';
    case 'succeeded':
      return 'Succeeded';
  }
}

/** The header button's label: "Queued · 2 ahead" while waiting, else the headline. */
export function deploymentButtonLabel(d: Deployment, active?: ActiveDeployment): string {
  if (d.status === 'queued') {
    return d.ahead === undefined ? 'Queued' : `Queued · ${d.ahead} ahead`;
  }
  return deploymentHeadline(d, active);
}

/** True while the Deployment still holds a place in the queue. */
export function isActive(d: Deployment): boolean {
  return d.status === 'queued' || d.status === 'running';
}
