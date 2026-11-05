// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import type { Compose, Environment, Project } from '../data/types';
import { FullscreenEditor } from './FullscreenEditor';

vi.mock('./YamlEditor', () => ({ YamlEditor: () => null }));

const compose = {
  createdAt: '2026-10-12T00:00:00Z', environmentId: 2, envSlug: 'production', id: 7, name: 'web',
  projectSlug: 'project', status: { running: 0, total: 0 }, updatedAt: '2026-10-12T00:00:00Z', yamlContent: '',
} as Compose;
const environment = { composes: [], createdAt: '', id: 2, name: 'production', projectId: 1, variables: [] } as unknown as Environment;
const project = { createdAt: '', description: '', environmentCount: 1, environments: [], id: 1, lastUpdated: '', name: 'Project' } as Project;

function renderEditor(readOnly: boolean) {
  render(
    <MemoryRouter>
      <FullscreenEditor
        compose={compose} dirty={false} draft="" environment={environment} onChange={() => {}} onClose={() => {}}
        onDiscard={() => {}} onSave={() => {}} open project={project} readOnly={readOnly} saving={false}
      />
    </MemoryRouter>,
  );
}

describe('FullscreenEditor subtitle', () => {
  it('says edits are shared to someone who can edit', () => {
    renderEditor(false);
    expect(screen.getByText(/edits are shared/)).toBeTruthy();
  });

  it('says the file is read-only to someone who cannot', () => {
    renderEditor(true);
    expect(screen.queryByText(/edits are shared/)).toBeNull();
    expect(screen.getByText('Read-only: only an admin changes docker-compose.yml.')).toBeTruthy();
  });
});
