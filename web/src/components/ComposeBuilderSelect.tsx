import { Alert, App, Card, Flex, Select, Typography } from 'antd';
import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import type { ComposePath } from '../data/api';
import { api } from '../data/api';
import { yamlServices } from '../data/source-view';
import type { ComposeBuilderChoice } from '../data/types';
import { formatLimits } from '../pages/builders-view';

const { Text } = Typography;

const DOCKER_DEFAULT_NAME = 'Docker default';

/** A choice as a Select value: `default`, `docker` or `builder:<id>`. */
function toValue(choice: ComposeBuilderChoice): string {
  return choice.mode === 'builder' ? `builder:${choice.builderId}` : choice.mode;
}

function fromValue(value: string): ComposeBuilderChoice {
  if (value.startsWith('builder:')) {
    return { builderId: Number(value.slice('builder:'.length)), mode: 'builder' };
  }
  return { builderId: 0, mode: value as 'default' | 'docker' };
}

/**
 * The Builder this Compose builds on. Shown only when a Service has `build:`;
 * a change applies to Deployments queued after it.
 */
export function ComposeBuilderSelect({ location, yaml }: { location: ComposePath; yaml: string }) {
  const builds = useMemo(() => yamlServices(yaml ?? '', true).length > 0, [yaml]);
  if (!builds) {
    return null;
  }
  return <BuilderChoice location={location} />;
}

function BuilderChoice({ location }: { location: ComposePath }) {
  const { message } = App.useApp();
  const builders = useSWR('/api/builders', () => api.listBuilders());
  const choiceKey = `/api/composes/${location.composeId}/builder`;
  const choice = useSWR(choiceKey, () => api.getComposeBuilder(location));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();

  const list = builders.data?.builders ?? [];
  const defaultId = builders.data?.defaultBuilderId ?? 0;
  const defaultName = defaultId === 0 ? DOCKER_DEFAULT_NAME : (list.find((b) => b.id === defaultId)?.name ?? DOCKER_DEFAULT_NAME);
  const options = [
    { label: `Use panel default (${defaultName})`, value: 'default' },
    { label: DOCKER_DEFAULT_NAME, value: 'docker' },
    ...list.map((b) => ({ label: formatLimits(b), value: `builder:${b.id}` })),
  ];

  const change = async (value: string) => {
    setSaving(true);
    setSaveError(undefined);
    try {
      await api.setComposeBuilder(location, fromValue(value));
      await choice.mutate();
      void message.success('Saved the Builder');
    } catch (reason: unknown) {
      setSaveError(reason instanceof Error ? reason.message : 'Could not save the Builder');
    } finally {
      setSaving(false);
    }
  };

  const loadError = builders.error ?? choice.error;

  return (
    <Card size="small" style={{ marginBottom: 16 }} title="Builder">
      <Flex gap={8} vertical>
        <Text type="secondary">
          Builds of this Compose run on this Builder. A change applies to Deployments queued after it.{' '}
          <Link to={paths.builders}>Manage Builders</Link>
        </Text>
        {loadError && <Alert showIcon title={loadError instanceof Error ? loadError.message : 'Could not load Builders'} type="error" />}
        {saveError && <Alert showIcon title={saveError} type="error" />}
        <Select
          aria-label="Builder"
          disabled={!builders.data || !choice.data}
          loading={saving || builders.isLoading || choice.isLoading}
          onChange={(value: string) => void change(value)}
          options={options}
          style={{ maxWidth: 480, width: '100%' }}
          value={choice.data ? toValue(choice.data) : undefined}
        />
      </Flex>
    </Card>
  );
}
