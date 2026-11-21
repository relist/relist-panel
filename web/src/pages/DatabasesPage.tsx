import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Collapse, Drawer, Flex, Form, Input, InputNumber, Select, Table, Tag, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { api } from '../data/api';
import type { Database, DatabaseEngine, DatabaseInput } from '../data/types';
import { pollInterval, statusMeta } from './databases-view';

const { Text } = Typography;

export const DATABASES_KEY = '/api/databases';

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/** The engine a new Database starts on: PostgreSQL when offered. */
function defaultEngine(engines: DatabaseEngine[]): DatabaseEngine | undefined {
  return engines.find((engine) => engine.name === 'postgres') ?? engines[0];
}

function defaultVersion(engine: DatabaseEngine | undefined): string | undefined {
  return (engine?.versions.find((version) => version.default) ?? engine?.versions[0])?.tag;
}

/**
 * Databases: Postgres, MySQL, MariaDB and Redis servers the panel runs, one
 * container each (ADR-0039). Other Composes reach them on relist-network.
 */
export function DatabasesPage() {
  const { data, error, isLoading, mutate } = useSWR(DATABASES_KEY, () => api.listDatabases(), {
    refreshInterval: (rows) => pollInterval(rows),
  });
  const { data: engines } = useSWR('/api/databases/engines', () => api.listDatabaseEngines());
  const [creating, setCreating] = useState(false);

  const engineLabel = (name: string) => engines?.find((engine) => engine.name === name)?.label ?? name;

  const columns: TableColumnsType<Database> = [
    {
      key: 'name',
      render: (_, row) => <Link to={paths.database(row.id)}>{row.name}</Link>,
      title: 'Name',
    },
    {
      key: 'engine',
      render: (_, row) => `${engineLabel(row.engine)} ${row.version}`,
      title: 'Engine',
    },
    {
      key: 'status',
      render: (_, row) => {
        const meta = statusMeta(row.status);
        return <Tag color={meta.color}>{meta.label}</Tag>;
      },
      title: 'Status',
    },
    {
      key: 'host',
      render: (_, row) => <Text code copyable>{row.host}</Text>,
      title: 'Host',
    },
    {
      key: 'created',
      render: (_, row) => <RelativeTime value={row.createdAt} />,
      title: 'Created',
    },
  ];

  return (
    <>
      <PageHeader
        description="Postgres, MySQL, MariaDB and Redis servers the panel runs for you. A Service on relist-network reaches each one by its host."
        extra={
          <Flex gap={8}>
            <Button icon={<ReloadOutlined />} loading={isLoading} onClick={() => void mutate()}>
              Refresh
            </Button>
            <Button disabled={!engines} icon={<PlusOutlined />} onClick={() => setCreating(true)} type="primary">
              Create database
            </Button>
          </Flex>
        }
        title="Databases"
      />
      {error && (
        <Alert
          action={<Button onClick={() => void mutate()}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={failure(error, 'Could not load Databases')}
          type="error"
        />
      )}
      <Card variant="outlined">
        <Table<Database>
          columns={columns}
          dataSource={data ?? []}
          loading={isLoading}
          locale={{
            emptyText: isLoading || error ? ' ' : <EmptyState title="No Databases yet" description="Create one to get a host, a password and a connection URL." />,
          }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>
      {engines && (
        <CreateDrawer
          engines={engines}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            void mutate();
          }}
          open={creating}
        />
      )}
    </>
  );
}

type FormValues = { name: string; engine: string; version: string; memoryMb: number | null; cpus: number | null };

function CreateDrawer({
  engines,
  onClose,
  onCreated,
  open,
}: {
  engines: DatabaseEngine[];
  onClose: () => void;
  onCreated: () => void;
  open: boolean;
}) {
  const { message } = App.useApp();
  const [form] = Form.useForm<FormValues>();
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const engineName = Form.useWatch('engine', form);
  const selected = engines.find((engine) => engine.name === engineName);

  useEffect(() => {
    if (!open) return;
    setSaveError(undefined);
    const engine = defaultEngine(engines);
    form.resetFields();
    form.setFieldsValue({ engine: engine?.name, version: defaultVersion(engine) });
  }, [open, engines, form]);

  const submit = async (values: FormValues) => {
    const input: DatabaseInput = { engine: values.engine, name: values.name.trim(), version: values.version };
    if (values.memoryMb) input.memoryMb = values.memoryMb;
    if (values.cpus) input.cpus = values.cpus;
    setBusy(true);
    setSaveError(undefined);
    try {
      const created = await api.createDatabase(input);
      void message.success(`Creating ${created.name}`);
      onCreated();
    } catch (reason: unknown) {
      setSaveError(failure(reason, 'Could not create the Database'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer
      destroyOnHidden
      extra={
        <Button loading={busy} onClick={() => form.submit()} type="primary">
          Create
        </Button>
      }
      onClose={onClose}
      open={open}
      size="default"
      title="Create database"
    >
      {saveError && <Alert showIcon style={{ marginBottom: 16 }} title={saveError} type="error" />}
      <Form<FormValues> form={form} layout="vertical" onFinish={(values) => void submit(values)} requiredMark={false}>
        <Form.Item
          label="Name"
          name="name"
          rules={[
            { message: 'Name must be 1–63 characters', required: true, whitespace: true },
            { max: 63, message: 'Name must be 1–63 characters' },
          ]}
        >
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item label="Engine" name="engine" rules={[{ required: true }]}>
          <Select
            onChange={(name: string) =>
              form.setFieldsValue({ version: defaultVersion(engines.find((engine) => engine.name === name)) })
            }
            options={engines.map((engine) => ({ label: engine.label, value: engine.name }))}
          />
        </Form.Item>
        <Form.Item label="Version" name="version" rules={[{ required: true }]}>
          <Select options={(selected?.versions ?? []).map((version) => ({ label: version.tag, value: version.tag }))} />
        </Form.Item>
        <Collapse
          ghost
          items={[
            {
              children: (
                <>
                  <Form.Item extra="Empty means no limit." label="Memory" name="memoryMb">
                    <InputNumber min={64} step={256} style={{ width: '100%' }} suffix="MB" />
                  </Form.Item>
                  <Form.Item extra="Empty means no limit." label="CPU" name="cpus">
                    <InputNumber min={0.1} step={0.1} style={{ width: '100%' }} suffix="cores" />
                  </Form.Item>
                </>
              ),
              key: 'limits',
              label: 'Limits',
            },
          ]}
        />
      </Form>
    </Drawer>
  );
}
