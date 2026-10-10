import { ThunderboltOutlined, WarningOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Select, Skeleton, Typography, theme } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import { usePanel } from '../data/store';
import type { Compose, Environment, ServiceBindings, VariableSet } from '../data/types';
import { bindingRows, setName } from './bindings-view';
import { EffectiveEnvModal } from './EffectiveEnvModal';
import { EmptyState } from './primitives';

const { Text } = Typography;

type Loaded = { composeId: number; bindings: ServiceBindings[]; services: string[]; sets: VariableSet[] };

/**
 * The Compose's Service bindings: one row per Service in the saved YAML (the
 * list the Domains form uses) with a multi-select of the Environment's sets.
 * Tag order is binding order. Bindings for Services the YAML no longer has are
 * listed apart and can only be removed. Each write refreshes the Compose, so
 * the redeploy banner follows `variablesChangedAt`.
 */
export function ComposeBindingsCard({
  compose,
  environment,
  onError,
}: {
  compose: Compose;
  environment: Environment;
  onError: (reason: unknown) => void;
}) {
  const { loadBindings, loadDomains, loadVariableSets, saveBindings, isPending } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [data, setData] = useState<Loaded>();
  const [error, setError] = useState<string>();
  const [effectiveFor, setEffectiveFor] = useState<string>();
  const request = useRef(0);
  const composeId = compose.id;
  const environmentId = environment.id;

  const reload = useCallback(async () => {
    const mine = ++request.current;
    try {
      const [sets, bindings, domains] = await Promise.all([
        loadVariableSets(environmentId),
        loadBindings(composeId),
        loadDomains(composeId),
      ]);
      if (request.current === mine) {
        setData({ bindings, composeId, services: domains.services, sets });
        setError(undefined);
      }
    } catch (reason: unknown) {
      if (request.current === mine) setError(reason instanceof Error ? reason.message : 'Could not load the bindings');
    }
  }, [composeId, environmentId, loadBindings, loadDomains, loadVariableSets]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const current = data?.composeId === composeId ? data : undefined;

  const bind = async (service: string, setIds: number[]) => {
    try {
      await saveBindings(composeId, service, setIds);
      message.success(setIds.length === 0 ? `Unbound all sets from ${service}` : `Updated bindings for ${service}`);
    } catch (reason: unknown) {
      onError(reason);
    }
    await reload();
  };

  let body;
  if (!current) {
    body = error ? (
      <Alert action={<Button onClick={() => void reload()}>Retry</Button>} showIcon title={error} type="error" />
    ) : (
      <Skeleton active paragraph={{ rows: 2 }} title={false} />
    );
  } else if (current.sets.length === 0) {
    // Nothing to bind yet: the sets are made in the Environment's Variables tab.
    body = (
      <EmptyState
        description="Create a Variable Set in this Environment's Variables tab, then bind it to a Service here."
        title="No Variable Sets to bind"
      />
    );
  } else if (current.services.length === 0 && bindingRows(current.services, current.bindings).stale.length === 0) {
    body = <EmptyState description="Save a Compose file with Services, then bind sets to them here." title="No Services in the saved YAML" />;
  } else {
    const { live, stale } = bindingRows(current.services, current.bindings);
    const options = current.sets.map((set) => ({ label: set.name, value: set.id }));
    const rowStyle = { borderBottom: `1px solid ${token.colorBorderSecondary}`, padding: '12px 0' };
    body = (
      <>
        {error && <Alert showIcon style={{ marginBottom: 12 }} title={error} type="error" />}
        {live.map((row) => (
          <Flex align="center" gap={12} key={row.service} style={rowStyle} wrap>
            <Text strong style={{ fontFamily: CODE_FONT_STACK, minWidth: 120 }}>
              {row.service}
            </Text>
            <Select<number[]>
              aria-label={`Variable Sets for ${row.service}`}
              loading={isPending(`save-bindings:${composeId}:${row.service}`)}
              mode="multiple"
              onChange={(ids) => void bind(row.service, ids)}
              optionFilterProp="label"
              options={options}
              placeholder="No sets bound"
              style={{ flex: '1 1 240px', minWidth: 0 }}
              value={row.setIds}
            />
            <Button icon={<ThunderboltOutlined />} onClick={() => setEffectiveFor(row.service)}>
              Effective env
            </Button>
          </Flex>
        ))}
        {stale.map((row) => (
          <Flex align="center" gap={12} key={row.service} style={rowStyle} wrap>
            <Flex align="center" gap={6} style={{ minWidth: 120 }}>
              <WarningOutlined style={{ color: token.colorWarning }} />
              <Text strong style={{ fontFamily: CODE_FONT_STACK }}>
                {row.service}
              </Text>
            </Flex>
            <Text style={{ flex: '1 1 240px' }} type="warning">
              not in YAML · bound to {row.setIds.map((id) => setName(current.sets, id)).join(', ')}
            </Text>
            <Button
              danger
              loading={isPending(`save-bindings:${composeId}:${row.service}`)}
              onClick={() => void bind(row.service, [])}
            >
              Remove
            </Button>
          </Flex>
        ))}
      </>
    );
  }

  return (
    <>
      <Card style={{ marginTop: 16 }} title="Service bindings" variant="outlined">
        <Text style={{ display: 'block', marginBottom: 12 }} type="secondary">
          Variable Sets delivered to a Service's containers, later sets overriding earlier ones. Tag order is binding order. Changes
          take effect on the next deploy.
        </Text>
        {body}
      </Card>
      <EffectiveEnvModal composeId={composeId} onClose={() => setEffectiveFor(undefined)} service={effectiveFor} />
    </>
  );
}
