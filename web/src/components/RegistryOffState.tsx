import { Link } from 'react-router';

import { paths } from '../app/paths';
import { EmptyState } from './primitives';

export const REGISTRY_KEY = '/api/registry';

/** Repositories and Push credentials have nothing to show until the Registry runs. */
export function RegistryOffState() {
  return (
    <EmptyState
      action={<Link to={`${paths.registry}?tab=settings`}>Open Settings</Link>}
      description="Turn it on in Settings so CI can push images to this host."
      title="Registry is off"
    />
  );
}
