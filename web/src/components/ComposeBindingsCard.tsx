import { ThunderboltOutlined, WarningOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Select, Skeleton, Typography, theme } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import { usePanel } from '../data/store';
import type { Compose, ServiceBindings, VariableSet } from '../data/types';
import { bindingRows, setName } from './bindings-view';
import { EffectiveEnvModal } from './EffectiveEnvModal';
import { EmptyState } from './primitives';

const { Text } = Typography;

type Loaded = { composeId: number; bindings: ServiceBindings[]; services: string[] };

/**
 * The Compose's Service bindings: one row per Service in the saved YAML (the
 * list the Domains form uses) with a multi-select of this Compose's sets (the Variables tab owns the list and passes it in).
 * Tag order is binding order. Bindings for Services the YAML no longer has are
 * listed apart and can only be removed. Each write refreshes the Compose, so
 * the redeploy banner follows `variablesChangedAt`. The bindings reload when
 * the sets change (a deleted set takes its bindings with it); a binding change
 * asks the tab to reload the sets, whose "bound to" tags it alters.
 */
export function ComposeBindingsCard({
  compose,
  onChanged,
  onError,
  sets,
}: {
  compose: Compose;
  /** Called after a binding write, to reload the sets' "bound to" tags. */
  onChanged?: () => void;
  onError: (reason: unknown) => void;
  /** This Compose's sets; `undefined` while they load. */
  sets?: VariableSet[];
}) {
  const { loadBindings, loadDomains, saveBindings, isPending } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [data, setData] = useState<Loaded>();
  const [error, setError] = useState<string>();
  const [effectiveFor, setEffectiveFor] = useState<string>();
  const request = useRef(0);
  // The Select shows what the operator picked, not the last server answer: a second pick made before
  // the first save returns builds on the first. Saves for one Service run in order, one at a time.
  const [draft, setDraft] = useState<Record<string, number[]>>({});
  const chains = useRef<Record<string, Promise<void>>>({});
  const waiting = useRef<Record<string, number>>({});
  const composeId = compose.id;

  const reload = useCallback(async () => {
    const mine = ++request.current;
    try {
      const [bindings, domains] = await Promise.all([loadBindings(composeId), loadDomains(composeId)]);
      if (request.current === mine) {
        setData({ bindings, composeId, services: domains.services });
        setError(undefined);
      }
    } catch (reason: unknown) {
      if (request.current === mine) setError(reason instanceof Error ? reason.message : 'Could not load the bindings');
    }
  }, [composeId, loadBindings, loadDomains]);

  // Reload when the sets change as well: deleting a set removes its bindings.
  useEffect(() => {
    void reload();
  }, [reload, sets]);

  const current = data?.composeId === composeId ? data : undefined;

  const bind = (service: string, setIds: number[]) => {
    setDraft((previous) => ({ ...previous, [service]: setIds }));
    waiting.current[service] = (waiting.current[service] ?? 0) + 1;
    const save = async () => {
      let failed = false;
      try {
        await saveBindings(composeId, service, setIds);
        message.success(setIds.length === 0 ? `Unbound all sets from ${service}` : `Updated bindings for ${service}`);
      } catch (reason: unknown) {
        failed = true;
        onError(reason);
      }
      waiting.current[service] -= 1;
      // Only the last save of a burst hands the row back to the server's answer.
      if (waiting.current[service] === 0 || failed) {
        await reload();
        onChanged?.();
        if (waiting.current[service] === 0) {
          setDraft((previous) => {
            const { [service]: _done, ...rest } = previous;
            return rest;
          });
        }
      }
    };
    chains.current[service] = (chains.current[service] ?? Promise.resolve()).then(save);
  };

  let body;
  if (!current) {
    body = error ? (
      <Alert action={<Button onClick={() => void reload()}>Retry</Button>} showIcon title={error} type="error" />
    ) : (
      <Skeleton active paragraph={{ rows: 2 }} title={false} />
    );
  } else if (!sets) {
    body = <Skeleton active paragraph={{ rows: 2 }} title={false} />;
  } else if (sets.length === 0) {
    // Nothing to bind yet: the sets are made in the card above.
    body = (
      <EmptyState
        description="Create a Variable Set in the Variable Sets card above, then bind it to a Service here."
        title="No Variable Sets to bind"
      />
    );
  } else if (current.services.length === 0 && bindingRows(current.services, current.bindings).stale.length === 0) {
    body = <EmptyState description="Save a Compose file with Services, then bind sets to them here." title="No Services in the saved YAML" />;
  } else {
    const { live, stale } = bindingRows(current.services, current.bindings);
    const options = sets.map((set) => ({ label: set.name, value: set.id }));
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
              onChange={(ids) => bind(row.service, ids)}
              optionFilterProp="label"
              options={options}
              placeholder="No sets bound"
              style={{ flex: '1 1 240px', minWidth: 0 }}
              value={draft[row.service] ?? row.setIds}
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
              not in YAML · bound to {row.setIds.map((id) => setName(sets, id)).join(', ')}
            </Text>
            <Button
              danger
              loading={isPending(`save-bindings:${composeId}:${row.service}`)}
              onClick={() => bind(row.service, [])}
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
