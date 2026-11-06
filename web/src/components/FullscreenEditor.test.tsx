// @vitest-environment jsdom
import '../test/dom-setup';

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import type { Compose, ComposeVariable, Environment, EnvironmentVariable, Project } from '../data/types';
import { FullscreenEditor } from './FullscreenEditor';

vi.mock('./YamlEditor', () => ({ YamlEditor: () => null }));

const compose = {
  createdAt: '2026-10-12T00:00:00Z', environmentId: 2, envSlug: 'production', id: 7, name: 'web',
  projectSlug: 'project', status: { running: 0, total: 0 }, updatedAt: '2026-10-12T00:00:00Z', yamlContent: '',
} as Compose;
const environment = { composes: [], createdAt: '', id: 2, name: 'production', projectId: 1, variables: [] } as unknown as Environment;
const project = { createdAt: '', description: '', environmentCount: 1, environments: [], id: 1, lastUpdated: '', name: 'Project' } as Project;

const envVar = (key: string, value: string): EnvironmentVariable =>
  ({ environmentId: 2, id: 1, key, secret: false, updatedAt: '', value });
const composeVar = (key: string, value: string): ComposeVariable =>
  ({ composeId: 7, id: 1, key, secret: false, value });

function renderEditor(
  readOnly: boolean,
  options: { composeVariables?: ComposeVariable[] | undefined; composeVariablesError?: string; variables?: EnvironmentVariable[] } = {},
) {
  // `composeVariables: undefined` means "not loaded yet", so only an absent key defaults to none.
  const composeVariables = 'composeVariables' in options ? options.composeVariables : [];
  const { composeVariablesError, variables = [] } = options;
  render(
    <MemoryRouter>
      <FullscreenEditor
        compose={compose} composeVariables={composeVariables} composeVariablesError={composeVariablesError} dirty={false} draft=""
        environment={{ ...environment, variables }} onChange={() => {}} onClose={() => {}}
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

describe('FullscreenEditor variables rail', () => {
  it('lists a Compose Variable even when the Environment has none', () => {
    renderEditor(false, { composeVariables: [composeVar('APP_BASE_URL', 'https://x')] });
    expect(screen.getByText('APP_BASE_URL')).toBeTruthy();
    expect(screen.getByText(/1 variable ·/)).toBeTruthy();
    expect(screen.queryByText('No variables yet')).toBeNull();
  });

  it('shows one row per key, the Compose value winning, tagged compose', () => {
    renderEditor(false, {
      composeVariables: [composeVar('LOG_LEVEL', 'debug')],
      variables: [envVar('LOG_LEVEL', 'info'), envVar('TZ', 'UTC')],
    });
    expect(screen.getAllByText('LOG_LEVEL')).toHaveLength(1);
    expect(screen.getByText('debug')).toBeTruthy();
    expect(screen.queryByText('info')).toBeNull();
    expect(screen.getByText('compose')).toBeTruthy();
    expect(screen.getByText(/2 variables ·/)).toBeTruthy();
  });

  it('shows the empty state when neither layer has a variable', () => {
    renderEditor(false);
    expect(screen.getByText('No variables yet')).toBeTruthy();
    expect(screen.getByText(/0 variables ·/)).toBeTruthy();
  });

  it('filters keys', () => {
    renderEditor(false, { composeVariables: [composeVar('DB_URL', 'a')], variables: [envVar('TZ', 'UTC')] });
    fireEvent.change(screen.getByPlaceholderText('Filter keys'), { target: { value: 'tz' } });
    expect(screen.getByText('TZ')).toBeTruthy();
    expect(screen.queryByText('DB_URL')).toBeNull();
  });

  it('filters a Compose Variable too', () => {
    renderEditor(false, { composeVariables: [composeVar('DB_URL', 'a')], variables: [envVar('TZ', 'UTC')] });
    fireEvent.change(screen.getByPlaceholderText('Filter keys'), { target: { value: 'db' } });
    expect(screen.getByText('DB_URL')).toBeTruthy();
    expect(screen.queryByText('TZ')).toBeNull();
  });

  it('masks a Compose secret that overrides a plain Environment value', () => {
    renderEditor(false, {
      composeVariables: [{ ...composeVar('TOKEN', 'compose-secret'), secret: true }],
      variables: [envVar('TOKEN', 'env-plain')],
    });
    expect(screen.queryByText('compose-secret')).toBeNull();
    expect(screen.queryByText('env-plain')).toBeNull();
    expect(screen.getByText('secret')).toBeTruthy();
  });

  it('does not claim an empty list while the Compose Variables load', () => {
    renderEditor(false, { composeVariables: undefined });
    expect(screen.queryByText('No variables yet')).toBeNull();
    expect(screen.getByText(/loading…/)).toBeTruthy();
  });

  it('warns when the Compose Variables fail to load, and still lists the Environment', () => {
    renderEditor(false, { composeVariables: undefined, composeVariablesError: 'boom', variables: [envVar('TZ', 'UTC')] });
    expect(screen.getByText('Compose Variables could not be loaded')).toBeTruthy();
    expect(screen.getByText('TZ')).toBeTruthy();
  });
});
