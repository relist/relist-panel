import { Alert, App, Button, Card, Flex, Segmented, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { api } from '../data/api';
import type { MigrationStatus, MigrationToken, TokenLifetime } from '../data/migration';
import { RelativeTime } from './primitives';

const { Paragraph, Text } = Typography;

const LIFETIMES: { label: string; value: TokenLifetime }[] = [
  { label: '1 hour', value: '1h' },
  { label: '24 hours', value: '24h' },
  { label: '7 days', value: '168h' },
];

/**
 * The old host's half of a server Migration (ADR-0018): create the token the
 * new panel claims. The token is shown once; the server keeps only its hash.
 */
export function ServerMigrationCard() {
  const { message } = App.useApp();
  const [status, setStatus] = useState<MigrationStatus>();
  const [lifetime, setLifetime] = useState<TokenLifetime>('24h');
  const [created, setCreated] = useState<MigrationToken>();
  const [pending, setPending] = useState<'create' | 'revoke'>();
  const [error, setError] = useState<string>();

  const load = useCallback(() => {
    api
      .migrationStatus()
      .then(setStatus)
      .catch((reason: unknown) => setError(reason instanceof Error ? reason.message : 'Could not read the migration status'));
  }, []);
  useEffect(load, [load]);

  const create = async () => {
    setPending('create');
    setError(undefined);
    try {
      setCreated(await api.createMigrationToken(lifetime));
      load();
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Could not create the token');
    } finally {
      setPending(undefined);
    }
  };

  const revoke = async () => {
    setPending('revoke');
    setError(undefined);
    try {
      await api.revokeMigrationToken();
      setCreated(undefined);
      message.success('Migration token revoked');
      load();
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Could not revoke the token');
    } finally {
      setPending(undefined);
    }
  };

  const knownExpiry = created?.expiresAt ?? status?.tokenExpiresAt ?? undefined;
  // An unclaimed token past its expiry cannot be claimed any more; a claimed
  // one stays, because the copy it started outlives it.
  const expired = !!knownExpiry && !status?.claimedBy && new Date(knownExpiry).getTime() <= Date.now();
  const expiresAt = expired ? undefined : knownExpiry;

  return (
    <Card title="Server migration">
      <Flex gap={16} vertical>
        <Paragraph style={{ margin: 0 }} type="secondary">
          To move everything this panel manages to another server, install an empty panel there with the same
          RELIST_DATA_DIR, choose Migrate from another panel on its setup screen, and give it this server&apos;s URL and a
          migration token. Nothing here stops until the new server starts the migration.
        </Paragraph>
        {error && <Alert showIcon title={error} type="error" />}
        {expired && <Alert showIcon title="The token expired. Create a new one." type="info" />}
        {created && !expired && (
          <Alert
            description={
              <Text code copyable={{ text: created.token }} style={{ wordBreak: 'break-all' }}>
                {created.token}
              </Text>
            }
            showIcon
            title="Shown once. Anyone with it can copy this server."
            type="warning"
          />
        )}
        <Flex align="center" gap={8} wrap>
          <Segmented<TokenLifetime> onChange={setLifetime} options={LIFETIMES} value={lifetime} />
          <Button loading={pending === 'create'} onClick={() => void create()} type="primary">
            Create migration token
          </Button>
          {expiresAt && (
            <>
              <Text type="secondary">
                Token expires <RelativeTime value={expiresAt} />
                {status?.claimedBy ? ` · claimed by ${status.claimedBy}` : ''}
              </Text>
              <Button danger loading={pending === 'revoke'} onClick={() => void revoke()}>
                Revoke
              </Button>
            </>
          )}
        </Flex>
      </Flex>
    </Card>
  );
}
