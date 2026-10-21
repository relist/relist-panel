import { Card, Flex, Result, Steps, Typography } from 'antd';

import { CenteredScreen } from '../app/RequireAuth';
import { RelativeTime } from '../components/primitives';
import type { MigrationStatus, MigrationStep } from '../data/migration';

const { Paragraph, Text } = Typography;

const LOCK_STEPS: { step: MigrationStep; title: string }[] = [
  { step: 'locked', title: 'Locked' },
  { step: 'stopping', title: 'Composes stopped' },
  { step: 'maintenance', title: 'Maintenance page up' },
  { step: 'ready', title: 'Ready to copy' },
];

/**
 * The full page a locked old host shows instead of the app (ADR-0018). It only
 * shows where the Migration is: cancelling it, or running this server again
 * once Migrated, is `migration resume` in a terminal (ADR-0019). The page
 * leaves by itself once the lock is gone.
 */
export function MigrationLockPage({ status }: { status: MigrationStatus }) {
  const migrated = status.step === 'migrated';
  const resume = (
    <Flex gap={4} vertical>
      <Text type="secondary">
        {migrated
          ? 'To run this server again, run this on it. Data written on the new server after the migration is not here.'
          : 'To cancel the migration, run this on this server. The Composes that were running start again here.'}
      </Text>
      <Text code copyable>
        docker compose exec panel relist-panel migration resume
      </Text>
    </Flex>
  );

  return (
    <CenteredScreen>
      <Card style={{ maxWidth: 640, width: '100%' }}>
        {migrated ? (
          <Result
            extra={resume}
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
                {/^https?:\/\//i.test(status.targetUrl) ? (
                  <Typography.Link href={status.targetUrl} rel="noreferrer" target="_blank">
                    {status.targetUrl}
                  </Typography.Link>
                ) : (
                  // Only an http(s) address becomes a link; anything else is shown, never followed.
                  <Text>{status.targetUrl}</Text>
                )}
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
            {resume}
          </Flex>
        )}
      </Card>
    </CenteredScreen>
  );
}
