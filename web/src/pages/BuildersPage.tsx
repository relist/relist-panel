import { DeleteOutlined, EditOutlined, PlayCircleOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, InputNumber, Modal, Popconfirm, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useState } from 'react';
import useSWR from 'swr';

import { formatBytes } from '../components/MetricChart';
import { DangerModal } from '../components/DangerModal';
import { PageHeader } from '../components/primitives';
import { ApiError, api } from '../data/api';
import type { Builder, BuilderInput, BuildersResponse } from '../data/types';
import { builderStatus, formatLimits } from './builders-view';

const { Text } = Typography;

const BUILDERS_KEY = '/api/builders';

export const DOCKER_DEFAULT_DESCRIPTION =
  "No CPU or memory limit. When a build uses up the host's memory, the kernel may kill a running app instead of the build. It can build FROM images that exist only on this host.";
export const CPU_HELP =
  'Tools inside the build still see every host CPU (nproc); the limit slows them down rather than shrinking their thread pools.';

/** The table's rows: Docker default (id 0) first, then every Builder. */
type Row = { kind: 'docker' } | { kind: 'builder'; builder: Builder };

const rowKey = (row: Row) => (row.kind === 'docker' ? 0 : row.builder.id);

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/**
 * Builders: resource-limited buildx builders. A Compose builds on the panel
 * default unless it picks one; Docker default is dockerd's own, unlimited
 * builder.
 */
export function BuildersPage() {
  const { message } = App.useApp();
  const { data, error, isLoading, mutate } = useSWR(BUILDERS_KEY, () => api.listBuilders());
  const [editing, setEditing] = useState<Builder | 'new'>();
  const [deleting, setDeleting] = useState<Builder>();
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const [busyAction, setBusyAction] = useState<string>();

  const reload = () => void mutate();

  // Each row action keeps its own spinner and reports a refusal on the page.
  const act = async (key: string, run: () => Promise<unknown>, done: string) => {
    setBusyAction(key);
    setActionError(undefined);
    try {
      await run();
      void message.success(done);
      reload();
    } catch (reason: unknown) {
      setActionError(failure(reason, 'Something went wrong'));
    } finally {
      setBusyAction(undefined);
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    setDeleteBusy(true);
    setActionError(undefined);
    try {
      await api.deleteBuilder(deleting.id);
      void message.success(`Deleted ${deleting.name}`);
      reload();
    } catch (reason: unknown) {
      setActionError(failure(reason, 'Could not delete the Builder'));
    } finally {
      setDeleteBusy(false);
      setDeleting(undefined);
    }
  };

  const defaultId = data?.defaultBuilderId ?? 0;
  const rows: Row[] = data ? [{ kind: 'docker' }, ...data.builders.map((builder) => ({ builder, kind: 'builder' as const }))] : [];

  const setDefaultButton = (id: number, name: string) =>
    id === defaultId ? null : (
      <Button
        aria-label={`Set ${name} as default`}
        loading={busyAction === `default-${id}`}
        onClick={() => void act(`default-${id}`, () => api.setDefaultBuilder(id), `${name} is now the panel default`)}
        size="small"
      >
        Set as default
      </Button>
    );

  const columns: TableColumnsType<Row> = [
    {
      key: 'name',
      render: (_, row) => {
        const name = row.kind === 'docker' ? 'Docker default' : row.builder.name;
        const id = rowKey(row);
        return (
          <Flex gap={4} vertical>
            <Flex align="center" gap={8}>
              <Text strong>{name}</Text>
              {id === defaultId && <Tag color="blue">Default</Tag>}
            </Flex>
            {row.kind === 'docker' && (
              <Text style={{ fontSize: 12 }} type="secondary">
                {DOCKER_DEFAULT_DESCRIPTION}
              </Text>
            )}
          </Flex>
        );
      },
      title: 'Name',
      width: '34%',
    },
    {
      key: 'limits',
      render: (_, row) =>
        row.kind === 'docker' ? (
          <Text type="secondary">No limits</Text>
        ) : (
          formatLimits(row.builder).replace(/^.* \((.*)\)$/, '$1')
        ),
      title: 'Limits',
    },
    {
      key: 'status',
      render: (_, row) => {
        if (row.kind === 'docker') return <Text type="secondary">—</Text>;
        const status = builderStatus(row.builder);
        const tag = <Tag color={status.color}>{status.label}</Tag>;
        return row.builder.lastError ? <Tooltip title={row.builder.lastError}>{tag}</Tooltip> : tag;
      },
      title: 'Status',
    },
    {
      key: 'cache',
      render: (_, row) =>
        row.kind === 'docker' ? <Text type="secondary">On the Docker page</Text> : <CacheSize id={row.builder.id} />,
      title: 'Cache',
    },
    {
      key: 'usedBy',
      render: (_, row) =>
        row.kind === 'docker' ? (
          <Text type="secondary">—</Text>
        ) : row.builder.usedBy?.length ? (
          row.builder.usedBy.join(', ')
        ) : (
          <Text type="secondary">Not chosen</Text>
        ),
      title: 'Chosen by',
    },
    {
      align: 'right',
      key: 'actions',
      render: (_, row) => {
        if (row.kind === 'docker') return setDefaultButton(0, 'Docker default');
        const { builder } = row;
        return (
          <Flex gap={4} justify="flex-end" wrap>
            {setDefaultButton(builder.id, builder.name)}
            <Tooltip title="Start">
              <Button
                aria-label={`Start ${builder.name}`}
                icon={<PlayCircleOutlined />}
                loading={busyAction === `start-${builder.id}`}
                onClick={() => void act(`start-${builder.id}`, () => api.startBuilder(builder.id), `Started ${builder.name}`)}
                size="small"
                type="text"
              />
            </Tooltip>
            <Popconfirm
              description="Deletes its build cache. The next build on it starts cold."
              okText="Prune"
              onConfirm={() => act(`prune-${builder.id}`, () => api.pruneBuilder(builder.id), `Pruned the cache of ${builder.name}`)}
              title={`Prune the cache of ${builder.name}?`}
            >
              <Button loading={busyAction === `prune-${builder.id}`} size="small">
                Prune cache
              </Button>
            </Popconfirm>
            <Button
              aria-label={`Edit ${builder.name}`}
              icon={<EditOutlined />}
              onClick={() => setEditing(builder)}
              size="small"
              type="text"
            />
            <Button
              aria-label={`Delete ${builder.name}`}
              danger
              icon={<DeleteOutlined />}
              onClick={() => setDeleting(builder)}
              size="small"
              type="text"
            />
          </Flex>
        );
      },
      title: '',
    },
  ];

  return (
    <>
      <PageHeader
        description="Resource-limited places to build Images. Each Compose builds on the panel default unless it picks a Builder."
        extra={
          <Flex gap={8}>
            <Button icon={<ReloadOutlined />} loading={isLoading} onClick={reload}>
              Refresh
            </Button>
            <Button disabled={!data} icon={<PlusOutlined />} onClick={() => setEditing('new')} type="primary">
              Add Builder
            </Button>
          </Flex>
        }
        title="Builders"
      />

      {error && (
        <Alert
          action={<Button onClick={reload}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={failure(error, 'Could not load Builders')}
          type="error"
        />
      )}
      {actionError && (
        <Alert
          closable={{ onClose: () => setActionError(undefined) }}
          showIcon
          style={{ marginBottom: 16 }}
          title={actionError}
          type="error"
        />
      )}

      {data && <BuildKitImageCard data={data} onSaved={reload} />}

      <Card variant="outlined">
        <Table<Row> columns={columns} dataSource={rows} loading={isLoading} pagination={false} rowKey={rowKey} />
      </Card>

      {data && (
        <BuilderModal
          builder={editing}
          capacity={data.capacity}
          onClose={() => setEditing(undefined)}
          onSaved={(saved) => {
            setEditing(undefined);
            void message.success(`Saved ${saved.name}`);
            reload();
          }}
        />
      )}
      <DangerModal
        confirmWord={deleting?.name ?? ''}
        description="Removes its container and its build cache. Composes that pick it must pick another Builder first."
        loading={deleteBusy}
        okText="Delete Builder"
        onCancel={() => setDeleting(undefined)}
        onConfirm={() => void confirmDelete()}
        open={deleting !== undefined}
        title={`Delete ${deleting?.name ?? ''}?`}
      />
    </>
  );
}

/** A Builder's cache size, fetched after the table renders. */
function CacheSize({ id }: { id: number }) {
  const { data, error } = useSWR(`${BUILDERS_KEY}/${id}/disk-usage`, () => api.builderDiskUsage(id));
  if (error) return <Text type="secondary">Unavailable</Text>;
  if (!data) return <Text type="secondary">…</Text>;
  return <>{formatBytes(data.bytes)}</>;
}

function BuildKitImageCard({ data, onSaved }: { data: BuildersResponse; onSaved: () => void }) {
  const { message } = App.useApp();
  const [value, setValue] = useState(data.buildkitImage);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string>();

  useEffect(() => setValue(data.buildkitImage), [data.buildkitImage]);

  const save = async () => {
    setBusy(true);
    setSaveError(undefined);
    try {
      await api.setBuildKitImage(value.trim());
      void message.success('Saved the BuildKit image');
      onSaved();
    } catch (reason: unknown) {
      setSaveError(failure(reason, 'Could not save the BuildKit image'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card style={{ marginBottom: 16 }} variant="outlined">
      <Flex gap={8} vertical>
        <Flex align="center" gap={8}>
          <Text strong>BuildKit image</Text>
          {data.buildkitImage === '' && <Tag>Default</Tag>}
        </Flex>
        <Text type="secondary">
          Every Builder runs this image. Builders switch to a new one on their next build; a custom image is pulled when you
          save.
        </Text>
        {saveError && <Alert showIcon title={saveError} type="error" />}
        <Space.Compact style={{ width: '100%' }}>
          <Input
            aria-label="BuildKit image"
            onChange={(event) => setValue(event.target.value)}
            placeholder={data.defaultBuildkitImage}
            value={value}
          />
          <Button onClick={() => setValue('')}>Use default</Button>
          <Button loading={busy} onClick={() => void save()} type="primary">
            Save
          </Button>
        </Space.Compact>
      </Flex>
    </Card>
  );
}

type FormValues = { name: string; cpus: number | null; memoryMb: number | null; maxParallelism: number | null };

function BuilderModal({
  builder,
  capacity,
  onClose,
  onSaved,
}: {
  builder: Builder | 'new' | undefined;
  capacity: BuildersResponse['capacity'];
  onClose: () => void;
  onSaved: (saved: Builder) => void;
}) {
  const [form] = Form.useForm<FormValues>();
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const open = builder !== undefined;
  const isNew = builder === 'new';
  const current = builder === 'new' ? undefined : builder;

  useEffect(() => {
    if (!open) return;
    setSaveError(undefined);
    form.resetFields();
    if (current) {
      form.setFieldsValue({ cpus: current.cpus, maxParallelism: current.maxParallelism, memoryMb: current.memoryMb, name: current.name });
    }
  }, [open, current, form]);

  const cpuMessage = `CPU must be between 0.1 and ${capacity.cpus}, in steps of 0.1`;
  const memoryMessage = `Memory must be between 256 MB and ${capacity.memoryMb} MB`;

  const submit = async (values: FormValues) => {
    const input: BuilderInput = {
      cpus: values.cpus ?? 0,
      maxParallelism: values.maxParallelism ?? null,
      memoryMb: values.memoryMb ?? 0,
      name: values.name.trim(),
    };
    setBusy(true);
    setSaveError(undefined);
    try {
      onSaved(current ? await api.updateBuilder(current.id, input) : await api.createBuilder(input));
    } catch (reason: unknown) {
      if (reason instanceof ApiError && reason.field && reason.field in values) {
        form.setFields([{ errors: [reason.message], name: reason.field as keyof FormValues }]);
      } else {
        setSaveError(failure(reason, 'Could not save the Builder'));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      destroyOnHidden
      okButtonProps={{ loading: busy }}
      okText={isNew ? 'Add' : 'Save'}
      onCancel={onClose}
      onOk={() => form.submit()}
      open={open}
      title={isNew ? 'Add Builder' : `Edit ${current?.name ?? ''}`}
    >
      {saveError && <Alert showIcon style={{ marginBottom: 16 }} title={saveError} type="error" />}
      <Form<FormValues> form={form} layout="vertical" onFinish={(values) => void submit(values)} requiredMark={false}>
        <Form.Item
          label="Name"
          name="name"
          rules={[
            { message: 'Name must be 1–40 characters', required: true, whitespace: true },
            { max: 40, message: 'Name must be 1–40 characters' },
          ]}
        >
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item
          extra={CPU_HELP}
          label="CPU"
          name="cpus"
          rules={[
            {
              validator: (_, cpus: number | null) =>
                cpus != null && cpus >= 0.1 && cpus <= capacity.cpus && Math.abs(Math.round(cpus * 10) - cpus * 10) < 1e-6
                  ? Promise.resolve()
                  : Promise.reject(new Error(cpuMessage)),
            },
          ]}
        >
          <InputNumber step={0.1} style={{ width: '100%' }} suffix="cores" />
        </Form.Item>
        <Form.Item
          label="Memory"
          name="memoryMb"
          rules={[
            {
              validator: (_, mb: number | null) =>
                mb != null && Number.isInteger(mb) && mb >= 256 && mb <= capacity.memoryMb
                  ? Promise.resolve()
                  : Promise.reject(new Error(memoryMessage)),
            },
          ]}
        >
          <InputNumber step={256} style={{ width: '100%' }} suffix="MB" />
        </Form.Item>
        <Form.Item
          extra="How many build steps run at once. Empty uses BuildKit's default. Changing it restarts the Builder."
          label="Max parallel steps"
          name="maxParallelism"
          rules={[
            {
              validator: (_, n: number | null) =>
                n == null || (Number.isInteger(n) && n >= 1 && n <= 64)
                  ? Promise.resolve()
                  : Promise.reject(new Error('Max parallel steps must be empty or 1–64')),
            },
          ]}
        >
          <InputNumber placeholder="Empty = BuildKit default" style={{ width: '100%' }} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
