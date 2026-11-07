import { Alert, Button, Card, Checkbox, Flex, Form, InputNumber, Modal, Radio, Switch, Table, Tag, TimePicker, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import dayjs, { type Dayjs } from 'dayjs';
import { useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { RegistryCleanupRun } from '../data/types';
import { formatBytes } from './MetricChart';
import { RelativeTime } from './primitives';

const { Text } = Typography;

export const REGISTRY_CLEANUP_KEY = '/api/registry/cleanup';

const WEEKDAYS = [
  { label: 'Mon', value: 'mon' },
  { label: 'Tue', value: 'tue' },
  { label: 'Wed', value: 'wed' },
  { label: 'Thu', value: 'thu' },
  { label: 'Fri', value: 'fri' },
  { label: 'Sat', value: 'sat' },
  { label: 'Sun', value: 'sun' },
];

const RESULT = {
  failed: { color: 'error', label: 'Failed' },
  interrupted: { color: 'warning', label: 'Interrupted' },
  ok: { color: 'success', label: 'OK' },
  running: { color: 'processing', label: 'Running' },
} as const;

interface CleanupForm {
  enabled: boolean;
  keep: number;
  frequency: 'daily' | 'weekly';
  weekdays: string[];
  time: Dayjs;
}

function toTime(minute: number): Dayjs {
  return dayjs().hour(Math.floor(minute / 60)).minute(minute % 60).second(0);
}

/**
 * Registry cleanup: retention (keep the newest N tags per repository plus
 * every tag in use), then garbage collection with the Registry read-only.
 */
export function RegistryCleanupCard({ enabled: registryOn }: { enabled: boolean }) {
  const { data, error, mutate } = useSWR(REGISTRY_CLEANUP_KEY, () => api.getRegistryCleanup(), {
    refreshInterval: (latest) => (latest?.running ? 3000 : 0),
  });
  const [form] = Form.useForm<CleanupForm>();
  const frequency = Form.useWatch('frequency', form);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string>();
  const [preview, setPreview] = useState<Record<string, string[]>>();
  const [previewing, setPreviewing] = useState(false);
  const [running, setRunning] = useState(false);

  if (!data) {
    return error ? (
      <Alert showIcon title={error instanceof Error ? error.message : 'Could not load Registry cleanup'} type="error" />
    ) : (
      <Card loading title="Registry cleanup" variant="outlined" />
    );
  }

  const { settings } = data;
  const daily = settings.days.includes('daily');

  const save = async (values: CleanupForm) => {
    setSaving(true);
    setFailure(undefined);
    try {
      const days = values.frequency === 'daily' ? ['daily'] : values.weekdays;
      const minute = values.time.hour() * 60 + values.time.minute();
      await mutate(api.saveRegistryCleanup({ days, enabled: values.enabled, keep: values.keep, minute }), { revalidate: false });
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not save Registry cleanup');
    } finally {
      setSaving(false);
    }
  };

  const openPreview = async () => {
    setPreviewing(true);
    setFailure(undefined);
    try {
      setPreview(await api.previewRegistryCleanup());
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not preview Registry cleanup');
    } finally {
      setPreviewing(false);
    }
  };

  const run = async () => {
    setRunning(true);
    try {
      await api.runRegistryCleanup();
      setPreview(undefined);
      void mutate();
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not start Registry cleanup');
      setPreview(undefined);
    } finally {
      setRunning(false);
    }
  };

  const columns: TableColumnsType<RegistryCleanupRun> = [
    { dataIndex: 'startedAt', key: 'startedAt', render: (at: string) => <RelativeTime value={at} />, title: 'Started' },
    {
      dataIndex: 'trigger',
      key: 'trigger',
      render: (trigger: string) => (trigger === 'schedule' ? 'Schedule' : trigger.replace(/^user:?/, '') || 'A user'),
      title: 'Trigger',
    },
    {
      dataIndex: 'deleted',
      key: 'deleted',
      render: (deleted: string[]) => (deleted.length === 1 ? '1 tag' : `${deleted.length} tags`),
      title: 'Deleted',
    },
    { dataIndex: 'freedBytes', key: 'freedBytes', render: (bytes: number) => formatBytes(bytes), title: 'Freed' },
    {
      dataIndex: 'result',
      key: 'result',
      render: (result: RegistryCleanupRun['result']) => <Tag color={RESULT[result].color}>{RESULT[result].label}</Tag>,
      title: 'Result',
    },
  ];

  const previewRepos = preview ? Object.keys(preview).sort() : [];

  return (
    <Card
      extra={
        <Button disabled={!registryOn || data.running} loading={previewing || data.running} onClick={() => void openPreview()}>
          Run cleanup now
        </Button>
      }
      title="Registry cleanup"
      variant="outlined"
    >
      <Flex gap={16} vertical>
        {failure && <Alert closable onClose={() => setFailure(undefined)} showIcon title={failure} type="error" />}
        <Text type="secondary">
          Deletes every tag except the newest per repository and those a Compose runs or last deployed, then frees their
          layers. Pushes fail while the Registry is read-only for that step; pulls keep working.
        </Text>
        <Form
          form={form}
          initialValues={{
            enabled: settings.enabled,
            frequency: daily ? 'daily' : 'weekly',
            keep: settings.keep,
            time: toTime(settings.minute),
            weekdays: daily ? [] : settings.days,
          }}
          layout="vertical"
          onFinish={(values) => void save(values)}
          style={{ maxWidth: 520 }}
        >
          <Form.Item label="Automatic cleanup" name="enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item
            label="Keep newest tags per repository"
            name="keep"
            rules={[{ message: 'Keep at least 1', min: 1, required: true, type: 'number' }]}
          >
            <InputNumber min={1} precision={0} />
          </Form.Item>
          <Form.Item label="Runs" name="frequency">
            <Radio.Group
              options={[
                { label: 'Daily', value: 'daily' },
                { label: 'Weekly', value: 'weekly' },
              ]}
            />
          </Form.Item>
          {frequency === 'weekly' && (
            <Form.Item name="weekdays" rules={[{ message: 'Choose at least one day', min: 1, required: true, type: 'array' }]}>
              <Checkbox.Group options={WEEKDAYS} />
            </Form.Item>
          )}
          <Form.Item extra="Host time." label="At" name="time">
            <TimePicker allowClear={false} format="HH:mm" minuteStep={5} needConfirm={false} />
          </Form.Item>
          <Flex align="center" gap={12} wrap>
            <Button htmlType="submit" loading={saving}>
              Save cleanup
            </Button>
            {data.next && settings.enabled && (
              <Text type="secondary">
                Next run <RelativeTime value={data.next} />
              </Text>
            )}
          </Flex>
        </Form>
        <Table<RegistryCleanupRun>
          columns={columns}
          dataSource={data.runs}
          expandable={{
            expandedRowRender: (r) => (
              <pre style={{ fontSize: 12, margin: 0, whiteSpace: 'pre-wrap' }}>
                {r.deleted.length > 0 ? `Deleted: ${r.deleted.join(', ')}\n\n` : ''}
                {r.log || 'No log.'}
              </pre>
            ),
          }}
          locale={{ emptyText: 'No cleanup has run yet.' }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
          size="small"
          title={() => <Text strong>History</Text>}
        />
      </Flex>
      <Modal
        confirmLoading={running}
        okButtonProps={{ danger: previewRepos.length > 0 }}
        okText="Run cleanup"
        onCancel={() => setPreview(undefined)}
        onOk={() => void run()}
        open={preview !== undefined}
        title="Run Registry cleanup?"
      >
        {previewRepos.length === 0 ? (
          <p>Nothing to delete. Garbage collection still runs and frees layers no tag uses.</p>
        ) : (
          <Flex gap={8} vertical>
            <Text>These tags will be deleted:</Text>
            {previewRepos.map((repo) => (
              <div key={repo}>
                <Text strong>{repo}</Text>
                <Text style={{ display: 'block' }}>{preview![repo].join(', ')}</Text>
              </div>
            ))}
          </Flex>
        )}
        <Text style={{ display: 'block', marginTop: 12 }} type="secondary">
          Pushes fail while garbage collection runs.
        </Text>
      </Modal>
    </Card>
  );
}
