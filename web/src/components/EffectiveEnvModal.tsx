import { Alert, Button, Flex, Modal, Skeleton, Table, Tag, Typography } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import { usePanel } from '../data/store';
import type { EffectiveVariable, VariableSource } from '../data/types';
import { effectiveRows, sourceKind, sourceLabel, type EffectiveRow } from './bindings-view';
import { ValueCell } from './VariablesPanel';

const { Text } = Typography;

const SOURCE_COLOR: Record<ReturnType<typeof sourceKind>, string | undefined> = {
  compose: 'blue',
  environment: undefined,
  set: 'purple',
  yaml: 'orange',
};

function SourceTag({ source }: { source: VariableSource }) {
  return <Tag color={SOURCE_COLOR[sourceKind(source)]}>{sourceLabel(source)}</Tag>;
}

/**
 * One Service's effective environment, as the server resolves it: the winning
 * value per key, where it came from, and the layers it shadows. Precedence is
 * the server's; the only client mark reads the server's "yaml" override on a merged
 * row whose key the saved YAML also sets.
 */
export function EffectiveEnvModal({
  composeId,
  onClose,
  service,
}: {
  composeId: number;
  onClose: () => void;
  /** The Service to show; `undefined` keeps the modal closed. */
  service?: string;
}) {
  const { loadEffectiveVariables } = usePanel();
  const [state, setState] = useState<{ service: string; rows?: EffectiveVariable[]; error?: string }>();
  const request = useRef(0);

  const load = useCallback(
    async (name: string) => {
      const mine = ++request.current;
      setState({ service: name });
      try {
        const rows = await loadEffectiveVariables(composeId, name);
        if (request.current === mine) setState({ rows, service: name });
      } catch (reason: unknown) {
        if (request.current === mine) {
          setState({ error: reason instanceof Error ? reason.message : 'Could not load the effective environment', service: name });
        }
      }
    },
    [composeId, loadEffectiveVariables],
  );

  useEffect(() => {
    if (service) void load(service);
  }, [load, service]);

  const current = state?.service === service ? state : undefined;
  const rows: EffectiveRow[] = effectiveRows(current?.rows ?? []);

  return (
    <Modal
      destroyOnHidden
      footer={<Button onClick={onClose}>Close</Button>}
      onCancel={onClose}
      open={service !== undefined}
      title={`Effective env · ${service ?? ''}`}
      width={880}
    >
      <Text style={{ display: 'block', marginBottom: 16 }} type="secondary">
        What this Service's containers see, lowest layer first: Environment, Compose, then each bound set in binding order. A key
        the YAML sets itself wins over all of them.
      </Text>
      {!current || (!current.rows && !current.error) ? (
        <Skeleton active paragraph={{ rows: 4 }} title={false} />
      ) : current.error ? (
        <Alert action={<Button onClick={() => service && void load(service)}>Retry</Button>} showIcon title={current.error} type="error" />
      ) : (
        <Table<EffectiveRow>
          columns={[
            {
              dataIndex: 'key',
              key: 'key',
              render: (key: string) => <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{key}</Text>,
              title: 'Key',
            },
            {
              key: 'value',
              render: (_value, row) =>
                row.source === 'yaml' ? <Text type="secondary">defined in the YAML</Text> : <ValueCell variable={row} />,
              title: 'Value',
            },
            {
              key: 'source',
              render: (_value, row) => (
                <Flex gap={4} wrap>
                  <SourceTag source={row.source} />
                  {row.overriddenByYaml && <Tag color="orange">overridden by YAML</Tag>}
                </Flex>
              ),
              title: 'Source',
            },
            {
              key: 'overrides',
              render: (_value, row) =>
                row.overrides.length === 0 ? (
                  <Text type="secondary">—</Text>
                ) : (
                  <Flex gap={4} wrap>
                    {row.overrides.map((source, index) => (
                      <SourceTag key={`${index}:${sourceLabel(source)}`} source={source} />
                    ))}
                  </Flex>
                ),
              title: 'Overrides',
            },
          ]}
          dataSource={rows}
          locale={{ emptyText: <Text type="secondary">This Service gets no variables from the panel.</Text> }}
          pagination={false}
          rowKey={(row) => `${row.source === 'yaml' ? 'yaml' : 'merged'}:${row.key}`}
          scroll={{ x: 'max-content' }}
          size="small"
        />
      )}
    </Modal>
  );
}
