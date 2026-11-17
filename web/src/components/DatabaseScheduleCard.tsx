import { Alert, Button, Card, Checkbox, Flex, Form, InputNumber, Radio, Select, Switch, Typography } from 'antd';
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
  localMode: 'simple' | 'gfs';
  localKeepCount: number | null;
  localKeepDays: number | null;
  localGfsDays: number | null;
  localGfsWeeks: number | null;
  localGfsMonths: number | null;
  localGfsYears: number | null;
  /** 0 = none. */
  destinationId: number;
  remoteMode: 'simple' | 'gfs';
  remoteKeepCount: number | null;
  remoteKeepDays: number | null;
  remoteGfsDays: number | null;
  remoteGfsWeeks: number | null;
  remoteGfsMonths: number | null;
  remoteGfsYears: number | null;
}

type KeepName =
  | 'localKeepCount'
  | 'localKeepDays'
  | 'localGfsDays'
  | 'localGfsWeeks'
  | 'localGfsMonths'
  | 'localGfsYears'
  | 'remoteKeepCount'
  | 'remoteKeepDays'
  | 'remoteGfsDays'
  | 'remoteGfsWeeks'
  | 'remoteGfsMonths'
  | 'remoteGfsYears';

/**
 * An InputNumber whose blank means "no limit" in Simple mode — but "keep
 * nothing for this rule" in GFS, where the placeholder must say so.
 */
function KeepField({ extra, label, name, placeholder = 'No limit' }: { extra?: string; label: string; name: KeepName; placeholder?: string }) {
  return (
    <Form.Item extra={extra} label={label} name={name}>
      <InputNumber aria-label={label} min={1} placeholder={placeholder} precision={0} />
    </Form.Item>
  );
}

/** One Rotation set: the Simple/GFS switch and the fields of the mode on. */
function RotationSet({ mode, prefix }: { mode: 'simple' | 'gfs'; prefix: '' | 'Destination: ' }) {
  const names = (prefix === '' ? 'local' : 'remote') as 'local' | 'remote';
  const label = prefix === '' ? 'Rotation' : 'Destination: rotation';
  const field = (base: string) => (prefix === '' ? base : prefix + base.charAt(0).toLowerCase() + base.slice(1));
  return (
    <>
      <Form.Item label={label} name={`${names}Mode`} style={{ marginBottom: 8, marginTop: 8 }}>
        <Radio.Group
          aria-label={label}
          options={[
            { label: 'Simple', value: 'simple' },
            { label: 'GFS', value: 'gfs' },
          ]}
        />
      </Form.Item>
      <Flex gap={16} wrap>
        <KeepField label={field('Last N backups')} name={`${names}KeepCount`} placeholder={mode === 'gfs' ? 'Keep none' : 'No limit'} />
        {mode === 'gfs' ? (
          <>
            <KeepField label={field('Daily for D days')} name={`${names}GfsDays`} placeholder="Keep none" />
            <KeepField extra="ISO weeks, Monday first." label={field('Weekly for W weeks')} name={`${names}GfsWeeks`} placeholder="Keep none" />
            <KeepField label={field('Monthly for M months')} name={`${names}GfsMonths`} placeholder="Keep none" />
            <KeepField label={field('Yearly for Y years')} name={`${names}GfsYears`} placeholder="Keep none" />
          </>
        ) : (
          <KeepField label={field('At most X days')} name={`${names}KeepDays`} />
        )}
      </Flex>
    </>
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
  const destinationId = Form.useWatch('destinationId', form);
  const localMode = Form.useWatch('localMode', form) ?? 'simple';
  const remoteMode = Form.useWatch('remoteMode', form) ?? 'simple';
  const destinations = useSWR('/api/backup-destinations', () => api.listBackupDestinations());
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
      const gfs = values.localMode === 'gfs';
      const saved: DatabaseScheduleState = await api.saveDatabaseSchedule(db.id, {
        all: values.scope === 'all',
        databases: values.scope === 'all' ? [] : values.databases,
        days,
        destinationId: values.destinationId || null,
        enabled: values.enabled,
        localMode: values.localMode,
        localKeepCount: values.localKeepCount ?? null,
        // Each mode owns its fields: the other mode's are sent as null.
        localKeepDays: gfs ? null : (values.localKeepDays ?? null),
        localGfsDays: gfs ? (values.localGfsDays ?? null) : null,
        localGfsWeeks: gfs ? (values.localGfsWeeks ?? null) : null,
        localGfsMonths: gfs ? (values.localGfsMonths ?? null) : null,
        localGfsYears: gfs ? (values.localGfsYears ?? null) : null,
        minute,
        // The fields are only on screen with a destination; without one the
        // stored values are sent back unchanged.
        ...(values.destinationId
          ? {
              remoteMode: values.remoteMode,
              remoteKeepCount: values.remoteKeepCount ?? null,
              remoteKeepDays: values.remoteMode === 'gfs' ? null : (values.remoteKeepDays ?? null),
              remoteGfsDays: values.remoteMode === 'gfs' ? (values.remoteGfsDays ?? null) : null,
              remoteGfsWeeks: values.remoteMode === 'gfs' ? (values.remoteGfsWeeks ?? null) : null,
              remoteGfsMonths: values.remoteMode === 'gfs' ? (values.remoteGfsMonths ?? null) : null,
              remoteGfsYears: values.remoteMode === 'gfs' ? (values.remoteGfsYears ?? null) : null,
            }
          : {
              remoteMode: data.remoteMode,
              remoteKeepCount: data.remoteKeepCount,
              remoteKeepDays: data.remoteKeepDays,
              remoteGfsDays: data.remoteGfsDays,
              remoteGfsWeeks: data.remoteGfsWeeks,
              remoteGfsMonths: data.remoteGfsMonths,
              remoteGfsYears: data.remoteGfsYears,
            }),
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
            destinationId: data.destinationId ?? 0,
            enabled: data.enabled,
            localMode: data.localMode,
            localKeepCount: data.localKeepCount,
            localKeepDays: data.localKeepDays,
            localGfsDays: data.localGfsDays,
            localGfsWeeks: data.localGfsWeeks,
            localGfsMonths: data.localGfsMonths,
            localGfsYears: data.localGfsYears,
            remoteMode: data.remoteMode,
            remoteKeepCount: data.remoteKeepCount,
            remoteKeepDays: data.remoteKeepDays,
            remoteGfsDays: data.remoteGfsDays,
            remoteGfsWeeks: data.remoteGfsWeeks,
            remoteGfsMonths: data.remoteGfsMonths,
            remoteGfsYears: data.remoteGfsYears,
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
          <RotationSet mode={localMode} prefix="" />
          <Form.Item
            extra={
              destinations.data && destinations.data.length === 0
                ? 'No Backup destinations yet. Add one under Data → Backup destinations.'
                : 'Scheduled backups are also copied to this bucket.'
            }
            label="Copy to"
            name="destinationId"
            style={{ marginTop: 12 }}
          >
            <Select
              aria-label="Copy to"
              options={[
                { label: 'None', value: 0 },
                ...(destinations.data ?? []).map((item) => {
                  // A schedule can only copy to an SFTP server whose host key a Test trusted.
                  const untested = item.type === 'sftp' && item.sftp.hostKey === '';
                  return {
                    disabled: untested,
                    label: untested ? `${item.name} (Test this destination first)` : item.name,
                    value: item.id,
                  };
                }),
              ]}
            />
          </Form.Item>
          {destinationId ? (
            <>
              <Text strong>Keep in the destination</Text>
              <RotationSet mode={remoteMode} prefix="Destination: " />
            </>
          ) : null}
          <Text style={{ display: 'block', marginBottom: 12 }} type="secondary">
            Rotation deletes only scheduled backups that are not pinned. The newest successful one of each database is
            always kept. A blank Simple field is no limit; a blank GFS field keeps nothing for that rule.
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
