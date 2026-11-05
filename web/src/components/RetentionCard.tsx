import { App, Button, Card, Flex, InputNumber, Switch, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { ApiError } from '../data/api';
import { canSaveRetention } from '../pages/traefik-view';

const { Text } = Typography;

/**
 * How long something is kept: a number of days, or forever. Shared by the
 * Access log (Traefik page) and the Audit log.
 */
export function RetentionCard({
  keepForever: savedForever,
  onSave,
  onSaved,
  title = 'Retention',
  retentionDays: savedDays,
}: {
  keepForever: boolean;
  onSave: (input: { keepForever: boolean; retentionDays: number }) => Promise<unknown>;
  onSaved: () => void;
  retentionDays: number;
  title?: string;
}) {
  const { message } = App.useApp();
  const [days, setDays] = useState<number | null>(savedDays);
  const [forever, setForever] = useState(savedForever);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setDays(savedDays);
    setForever(savedForever);
  }, [savedDays, savedForever]);

  const save = async () => {
    if (days === null) return;
    setSaving(true);
    try {
      await onSave({ keepForever: forever, retentionDays: days });
      onSaved();
      void message.success('Retention saved');
    } catch (reason: unknown) {
      void message.error(reason instanceof ApiError ? reason.message : 'Could not save retention');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card title={title} variant="outlined">
      <Flex align="center" gap={16} wrap>
        <InputNumber
          aria-label="Days to keep"
          disabled={forever}
          min={1}
          onChange={(value) => setDays(typeof value === 'number' ? value : null)}
          precision={0}
          suffix="days"
          value={days}
        />
        <Flex align="center" gap={8}>
          <Switch aria-label="Keep forever" checked={forever} onChange={setForever} />
          <Text>Keep forever</Text>
        </Flex>
        <Button
          disabled={!canSaveRetention(days, forever, { keepForever: savedForever, retentionDays: savedDays })}
          loading={saving}
          onClick={() => void save()}
          type="primary"
        >
          Save
        </Button>
      </Flex>
    </Card>
  );
}
