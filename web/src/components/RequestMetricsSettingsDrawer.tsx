import { Alert, App, Button, Drawer, Flex, InputNumber, Typography } from 'antd';
import { useEffect, useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';

const { Text } = Typography;

export const REQUEST_SETTINGS_KEY = '/api/requests/settings';

/**
 * How long hourly request history is kept, 1 to 90 days (ADR-0035). The last
 * 24 hours are always kept at one-minute detail. Saving a lower value deletes
 * the older history within the hour, so that is confirmed first.
 */
export function RequestMetricsSettingsDrawer({
  onClose,
  onSaved,
  open,
}: {
  open: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { message, modal } = App.useApp();
  const { data, error, mutate } = useSWR(open ? REQUEST_SETTINGS_KEY : null, () => api.requestMetricSettings());
  const [days, setDays] = useState<number | null>(1);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();

  useEffect(() => {
    if (data && open) {
      setDays(data.retentionDays);
      setSaveError(undefined);
    }
  }, [data, open]);

  const valid = days !== null && Number.isInteger(days) && days >= 1 && days <= 90;
  const lowering = valid && data !== undefined && days < data.retentionDays;

  const save = async () => {
    if (!valid || days === null) return;
    setSaving(true);
    setSaveError(undefined);
    try {
      await mutate(await api.saveRequestMetricSettings({ retentionDays: days }), { revalidate: false });
      message.success('Saved request history settings');
      onSaved();
      onClose();
    } catch (reason) {
      setSaveError(reason instanceof Error ? reason.message : 'Could not save the settings');
    } finally {
      setSaving(false);
    }
  };

  const confirmAndSave = () => {
    if (!lowering || data === undefined) {
      void save();
      return;
    }
    modal.confirm({
      cancelText: 'Cancel',
      content: `History older than ${days} ${days === 1 ? 'day' : 'days'} will be deleted within the hour.`,
      okText: 'Save and delete',
      okButtonProps: { danger: true },
      onOk: () => save(),
      title: 'Keep less history?',
    });
  };

  return (
    <Drawer
      extra={
        <Button disabled={!valid || (data !== undefined && days === data.retentionDays)} loading={saving} onClick={confirmAndSave} type="primary">
          Save
        </Button>
      }
      onClose={onClose}
      open={open}
      size="default"
      title="Request history"
    >
      {error && <Alert showIcon style={{ marginBottom: 16 }} title="Could not load the settings" type="error" />}
      {saveError && <Alert showIcon style={{ marginBottom: 16 }} title={saveError} type="error" />}
      <Flex gap={8} vertical>
        <Text>Days of hourly history to keep</Text>
        <InputNumber aria-label="Days of hourly history to keep" max={90} min={1} onChange={setDays} precision={0} style={{ width: 160 }} value={days} />
        <Text type="secondary">
          The last 24 hours are always kept per minute. Older history is kept per hour for this many days, 1 to 90. The
          7, 30 and 90 day windows appear once the retention covers them.
        </Text>
      </Flex>
    </Drawer>
  );
}
