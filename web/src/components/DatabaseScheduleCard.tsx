import { Alert, Button, Card, Checkbox, Flex, Form, InputNumber, Radio, Switch, Typography } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { Database, DatabaseScheduleState } from '../data/types';
import { RelativeTime } from './primitives';
import { ScheduleFields, scheduleFormValues, scheduleFromValues, type ScheduleFormValues } from './ScheduleFields';

const { Text } = Typography;

interface ScheduleForm extends ScheduleFormValues {
  enabled: boolean;
  scope: 'all' | 'selected';
  databases: string[];
  localKeepCount: number | null;
  localKeepDays: number | null;
}

/** An InputNumber that is blank means no limit. */
function KeepField({ label, name }: { label: string; name: 'localKeepCount' | 'localKeepDays' }) {
  return (
    <Form.Item label={label} name={name}>
      <InputNumber aria-label={label} min={1} placeholder="No limit" precision={0} />
    </Form.Item>
  );
}

/**
 * A Database's backup schedule: when it runs, what it backs up and how many
 * of its Backups Rotation keeps on this server.
 */
export function DatabaseScheduleCard({ db }: { db: Database }) {
  const key = `/api/databases/${db.id}/schedule`;
  const { data, error, mutate } = useSWR(key, () => api.getDatabaseSchedule(db.id));
  const [form] = Form.useForm<ScheduleForm>();
  const scope = Form.useWatch('scope', form);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string>();
  const logical = useSWR(scope === 'selected' ? `${key}/logical` : null, () => api.databaseLogicalDatabases(db.id));

  if (!data) {
    return error ? (
      <Alert showIcon title={error instanceof Error ? error.message : 'Could not load the schedule'} type="error" />
    ) : (
      <Card loading title="Scheduled backups" variant="outlined" />
    );
  }

  const save = async (values: ScheduleForm) => {
    setSaving(true);
    setFailure(undefined);
    try {
      const { days, minute } = scheduleFromValues(values);
      const saved: DatabaseScheduleState = await api.saveDatabaseSchedule(db.id, {
        all: values.scope === 'all',
        databases: values.scope === 'all' ? [] : values.databases,
        days,
        enabled: values.enabled,
        localKeepCount: values.localKeepCount ?? null,
        localKeepDays: values.localKeepDays ?? null,
        minute,
        remoteKeepCount: data.remoteKeepCount,
        remoteKeepDays: data.remoteKeepDays,
      });
      await mutate(saved, { revalidate: false });
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not save the schedule');
    } finally {
      setSaving(false);
    }
  };

  // The saved names stay listed even when the database is gone, so the
  // Admin sees what the schedule would fail on.
  const names = [...new Set([...(logical.data?.databases ?? []), ...data.databases])].sort();

  return (
    <Card title="Scheduled backups" variant="outlined">
      <Flex gap={16} vertical>
        {data.lastError && <Alert showIcon title={data.lastError} type="warning" />}
        {failure && <Alert closable={{ onClose: () => setFailure(undefined) }} showIcon title={failure} type="error" />}
        <Form<ScheduleForm>
          form={form}
          initialValues={{
            databases: data.databases,
            enabled: data.enabled,
            localKeepCount: data.localKeepCount,
            localKeepDays: data.localKeepDays,
            scope: data.all ? 'all' : 'selected',
            ...scheduleFormValues(data.days, data.minute),
          }}
          layout="vertical"
          onFinish={(values) => void save(values)}
          style={{ maxWidth: 520 }}
        >
          <Form.Item label="Scheduled backups" name="enabled" valuePropName="checked">
            <Switch aria-label="Scheduled backups" />
          </Form.Item>
          <ScheduleFields timeZone={data.timeZone} />
          <Form.Item label="What to back up" name="scope">
            <Radio.Group>
              <Radio value="all">All databases</Radio>
              <Radio value="selected">Choose databases</Radio>
            </Radio.Group>
          </Form.Item>
          {scope === 'selected' && (
            <Form.Item name="databases" rules={[{ message: 'Choose at least one database', min: 1, required: true, type: 'array' }]}>
              <Checkbox.Group
                options={names.map((name) => ({ label: name, value: name }))}
                style={{ display: 'flex', flexDirection: 'column', gap: 4 }}
              />
            </Form.Item>
          )}
          <Text strong>Keep on this server</Text>
          <Flex gap={16} style={{ marginTop: 8 }} wrap>
            <KeepField label="Last N backups" name="localKeepCount" />
            <KeepField label="At most X days" name="localKeepDays" />
          </Flex>
          <Text style={{ display: 'block', marginBottom: 12 }} type="secondary">
            Rotation deletes only scheduled backups that are not pinned. The newest successful one of each database is
            always kept. Leave a field blank for no limit.
          </Text>
          <Flex align="center" gap={12} wrap>
            <Button htmlType="submit" loading={saving}>
              Save schedule
            </Button>
            {data.next && data.enabled && (
              <Text type="secondary">
                Next run <RelativeTime value={data.next} />
              </Text>
            )}
          </Flex>
        </Form>
      </Flex>
    </Card>
  );
}
