// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { RemovalPlanEntry } from '../data/types';
import { RemovalPlanList } from './RemovalPlanList';

const web: RemovalPlanEntry = {
  composeId: 1,
  directory: '/data/.composes/1',
  keepVolumes: [{ name: 'shared-db', reason: 'declared by Compose Api' }],
  name: 'Web',
  removeVolumes: ['web-data'],
};

describe('RemovalPlanList', () => {
  it('lists removed volumes, kept volumes with their reason, and the directory', () => {
    render(<RemovalPlanList entries={[web]} />);
    expect(screen.getByText('Volumes removed')).toBeTruthy();
    expect(screen.getByText('web-data')).toBeTruthy();
    expect(screen.getByText('Volumes kept (used elsewhere)')).toBeTruthy();
    expect(screen.getByText('shared-db')).toBeTruthy();
    expect(screen.getByText('declared by Compose Api')).toBeTruthy();
    expect(screen.getByText('Directory removed')).toBeTruthy();
    expect(screen.getByText('/data/.composes/1')).toBeTruthy();
  });

  it('names each Compose when a Project or Environment deletes several', () => {
    const other: RemovalPlanEntry = { ...web, composeId: 2, directory: '/data/.composes/2', keepVolumes: [], name: 'Api', removeVolumes: [] };
    render(<RemovalPlanList entries={[web, other]} />);
    expect(screen.getByText('Web')).toBeTruthy();
    expect(screen.getByText('Api')).toBeTruthy();
    expect(screen.getAllByText('Directory removed')).toHaveLength(2);
    expect(screen.getAllByText('Volumes removed')).toHaveLength(1);
  });

  it('shows a Compose entry error on its own line and still shows the directory', () => {
    render(<RemovalPlanList entries={[{ ...web, error: 'volume details unavailable', keepVolumes: [], removeVolumes: [] }]} />);
    expect(screen.getByText('volume details unavailable')).toBeTruthy();
    expect(screen.queryByText('Volumes removed')).toBeNull();
    expect(screen.getByText('/data/.composes/1')).toBeTruthy();
  });

  it('shows a failed load as a warning and no plan', () => {
    render(<RemovalPlanList error="Request failed (500)" />);
    expect(screen.getByText('Could not load what this delete removes.')).toBeTruthy();
    expect(screen.getByText(/Request failed \(500\)/)).toBeTruthy();
    expect(screen.queryByText('Directory removed')).toBeNull();
  });

  it('falls back to the directories the page knows when the plan could not load', () => {
    render(<RemovalPlanList error="Request failed (500)" fallbackDirectories={['/data/.composes/1']} />);
    expect(screen.getByText('Directory removed')).toBeTruthy();
    expect(screen.getByText('/data/.composes/1')).toBeTruthy();
  });

  it('shows a placeholder, not an empty plan, while loading', () => {
    const { container } = render(<RemovalPlanList loading />);
    expect(container.querySelector('.ant-skeleton')).toBeTruthy();
    expect(screen.queryByText('Directory removed')).toBeNull();
  });

  it('says so when there are no Composes', () => {
    render(<RemovalPlanList entries={[]} />);
    expect(screen.getByText('There are no Composes to tear down.')).toBeTruthy();
  });
});
