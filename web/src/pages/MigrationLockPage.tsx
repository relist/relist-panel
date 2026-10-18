import { Alert, App, Button, Card, Flex, Result, Steps, Typography } from 'antd';
import { useState } from 'react';

import { CenteredScreen } from '../app/RequireAuth';
import { DangerModal } from '../components/DangerModal';
import { RelativeTime } from '../components/primitives';
import { api } from '../data/api';
import type { MigrationStatus, MigrationStep } from '../data/migration';

const { Paragraph, Text } = Typography;

const LOCK_STEPS: { step: MigrationStep; title: string }[] = [
  { step: 'locked', title: 'Locked' },
  { step: 'stopping', title: 'Composes stopped' },
  { step: 'maintenance', title: 'Maintenance page up' },
  { step: 'ready', title: 'Ready to copy' },
];

/**
 * The full page a locked old host shows instead of the app (ADR-0018). While
 * locked, the only action is Cancel migration; once Migrated, Resume this
 * server. Both are the same typed `RESUME`.
 */
export function MigrationLockPage({ onResumed, status }: { onResumed: () => void; status: MigrationStatus }) {
  const { message } = App.useApp();
  const [confirming, setConfirming] = useState(false);
  const [resuming, setResuming] = useState(false);
  const [error, setError] = useState<string>();
  const migrated = status.step === 'migrated';
  const action = migrated ? 'Resume this server' : 'Cancel migration';

  const resume = async () => {
    setResuming(true);
    setError(undefined);
    try {
      await api.resumeMigration('RESUME');
      setConfirming(false);
      message.success(migrated ? 'This server is running again' : 'Migration canceled');
      onResumed();
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Could not resume this server');
    } finally {
      setResuming(false);
    }
  };

  return (
    <CenteredScreen>
      <Card style={{ maxWidth: 640, width: '100%' }}>
        {error && <Alert showIcon style={{ marginBottom: 16 }} title={error} type="error" />}
        {migrated ? (
          <Result
            extra={
              <Button danger onClick={() => setConfirming(true)}>
                {action}
              </Button>
            }
            status="success"
            subTitle={
              <Flex gap={4} vertical>
                {status.migratedAt && (
                  <Text type="secondary">
                    Migrated <RelativeTime value={status.migratedAt} />. Its Composes stay stopped and its domains show
                    a maintenance page. Turn this server off once DNS points at the new one.
                  </Text>
                )}
              </Flex>
            }
            title={
              <>
                Migrated to{' '}
                <Typography.Link href={status.targetUrl} target="_blank">
                  {status.targetUrl}
                </Typography.Link>
              </>
            }
          />
        ) : (
          <Flex gap={16} vertical>
            <Typography.Title level={3} style={{ margin: 0 }}>
              This server is being migrated
            </Typography.Title>
            <Paragraph style={{ margin: 0 }} type="secondary">
              Nothing can be changed here while another panel copies it.
              {status.claimedBy ? (
                <>
                  {' '}
                  Being copied by <Text strong>{status.claimedBy}</Text>.
                </>
              ) : null}
            </Paragraph>
            <Steps
              current={Math.max(0, LOCK_STEPS.findIndex((s) => s.step === status.step))}
              items={LOCK_STEPS.map((s) => ({ title: s.title }))}
              orientation="vertical"
              size="small"
            />
            <Flex justify="flex-end">
              <Button danger onClick={() => setConfirming(true)}>
                {action}
              </Button>
            </Flex>
          </Flex>
        )}
      </Card>
      <DangerModal
        confirmWord="RESUME"
        consequences={
          migrated ? (
            <Alert showIcon title="Data written on the new server after the migration is not here." type="warning" />
          ) : undefined
        }
        description={
          migrated
            ? 'The maintenance page goes away and the Composes that were running start again on this server.'
            : 'The new server stops copying. The maintenance page goes away and the Composes that were running start again here.'
        }
        loading={resuming}
        okText={action}
        onCancel={() => setConfirming(false)}
        onConfirm={() => void resume()}
        open={confirming}
        title={action}
      />
    </CenteredScreen>
  );
}
