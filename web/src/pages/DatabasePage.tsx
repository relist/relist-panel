import { EyeInvisibleOutlined, EyeOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Descriptions, Flex, Input, Tabs, Tag, Tooltip, Typography } from 'antd';
import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { DangerModal } from '../components/DangerModal';
import { PageHeader } from '../components/primitives';
import { api } from '../data/api';
import type { Database, DatabaseAction, DatabaseCredentials } from '../data/types';
import { canRepair, pollInterval, snippet, statusMeta } from './databases-view';

const { Text } = Typography;

const MASK = '••••••••';

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/** One Database: its status and actions, how to connect, its logs and settings. */
export function DatabasePage() {
  const { message } = App.useApp();
  const navigate = useNavigate();
  const databaseId = Number(useParams().databaseId);
  const key = `/api/databases/${databaseId}`;
  const { data, error, isLoading, mutate } = useSWR(key, () => api.getDatabase(databaseId), {
    refreshInterval: (row) => pollInterval(row ? [row] : undefined),
  });
  const [busy, setBusy] = useState<DatabaseAction>();
  const [actionError, setActionError] = useState<string>();

  const act = async (action: DatabaseAction, done: string) => {
    setBusy(action);
    setActionError(undefined);
    try {
      await api.databaseAction(databaseId, action);
      void message.success(done);
      await mutate();
    } catch (reason: unknown) {
      setActionError(failure(reason, 'Something went wrong'));
      void mutate();
    } finally {
      setBusy(undefined);
    }
  };

  const name = data?.name ?? 'Database';
  const meta = data ? statusMeta(data.status) : undefined;
  const wantsRunning = data?.desired === 'running';

  return (
    <>
      <PageHeader
        breadcrumb={[
          { title: <Link to={paths.overview}>Overview</Link> },
          { title: <Link to={paths.databases}>Databases</Link> },
          { title: name },
        ]}
        description={data ? `${data.image}` : undefined}
        extra={
          data && (
            <Flex gap={8} wrap>
              {wantsRunning ? (
                <Button loading={busy === 'stop'} onClick={() => void act('stop', `Stopped ${name}`)}>
                  Stop
                </Button>
              ) : (
                <Button loading={busy === 'start'} onClick={() => void act('start', `Started ${name}`)} type="primary">
                  Start
                </Button>
              )}
              <Button
                disabled={!wantsRunning}
                loading={busy === 'restart'}
                onClick={() => void act('restart', `Restarted ${name}`)}
              >
                Restart
              </Button>
              <Tooltip title="Pulls the tag again and applies this panel's container settings. Recreates the container on the same volume only if either changed; a stopped Database stays stopped.">
                <Button loading={busy === 'update-image'} onClick={() => void act('update-image', `Updated the image of ${name}`)}>
                  Update image
                </Button>
              </Tooltip>
              {canRepair(data) && (
                <Button loading={busy === 'repair'} onClick={() => void act('repair', `Repaired ${name}`)} type="primary">
                  Repair
                </Button>
              )}
            </Flex>
          )
        }
        tags={meta && <Tag color={meta.color}>{meta.label}</Tag>}
        title={name}
      />

      {error && (
        <Alert
          action={<Button onClick={() => void mutate()}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={failure(error, 'Could not load the Database')}
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
      {data?.status === 'volume_missing' && (
        <Alert
          description="Nothing was recreated, because an empty new volume would look like the data had never been lost. Delete this Database, or restore its data into a new one."
          showIcon
          style={{ marginBottom: 16 }}
          title="The data volume is gone"
          type="error"
        />
      )}
      {data?.settingsOutdated && (
        <Alert
          action={
            <Button
              loading={busy === 'apply-settings'}
              onClick={() => void act('apply-settings', `Applied the new settings to ${name}`)}
              size="small"
            >
              Apply settings
            </Button>
          }
          description={
            wantsRunning
              ? 'This panel version creates the container differently. Applying recreates the container on the same volume: a short restart. Nothing changes until you apply, or until Update image.'
              : 'This panel version creates the container differently. Applying takes effect on the next Start.'
          }
          showIcon
          style={{ marginBottom: 16 }}
          title="New container settings are available"
          type="info"
        />
      )}
      {data?.lastError && data.status !== 'volume_missing' && (
        <Alert
          description={canRepair(data) ? 'Repair tries again.' : undefined}
          showIcon
          style={{ marginBottom: 16 }}
          title={data.lastError}
          type="error"
        />
      )}

      {data && (
        <Tabs
          destroyOnHidden
          items={[
            { children: <ConnectionTab db={data} />, key: 'connection', label: 'Connection' },
            { children: <LogsTab id={databaseId} />, key: 'logs', label: 'Logs' },
            {
              children: (
                <SettingsTab
                  db={data}
                  onDeleted={() => {
                    void message.success(`Deleted ${name}`);
                    void navigate(paths.databases);
                  }}
                  onRenamed={() => void mutate()}
                />
              ),
              key: 'settings',
              label: 'Settings',
            },
          ]}
        />
      )}
      {isLoading && !data && !error && <Text type="secondary">Loading…</Text>}
    </>
  );
}

function ConnectionTab({ db }: { db: Database }) {
  const [creds, setCreds] = useState<DatabaseCredentials>();
  const [busy, setBusy] = useState(false);
  const [revealError, setRevealError] = useState<string>();

  // The password is fetched only on Reveal: every read is audited.
  const toggle = async () => {
    if (creds) {
      setCreds(undefined);
      return;
    }
    setBusy(true);
    setRevealError(undefined);
    try {
      setCreds(await api.databaseCredentials(db.id));
    } catch (reason: unknown) {
      setRevealError(failure(reason, 'Could not reveal the password'));
    } finally {
      setBusy(false);
    }
  };

  const hidden = <Text type="secondary">Reveal the password to see the URL.</Text>;
  return (
    <Flex gap={16} vertical>
      <Alert
        showIcon
        title="The Service that connects must be on relist-network. Use the stable host in config; the readable host follows renames."
        type="info"
      />
      {db.warnings.map((warning) => (
        <Alert key={warning} showIcon title={warning} type="warning" />
      ))}
      <Card variant="outlined">
        <Descriptions
          bordered
          column={1}
          items={[
            { children: <Text code copyable>{db.host}</Text>, key: 'host', label: 'Host (stable)' },
            {
              children: db.hostReadable ? <Text code copyable>{db.hostReadable}</Text> : <Text type="secondary">None</Text>,
              key: 'readable',
              label: 'Host (readable)',
            },
            { children: <Text code copyable>{String(db.port)}</Text>, key: 'port', label: 'Port' },
            { children: <Text code copyable>{db.user}</Text>, key: 'user', label: 'User' },
            ...(db.defaultDatabase
              ? [{ children: <Text code copyable>{db.defaultDatabase}</Text>, key: 'db', label: 'Default database' }]
              : []),
            {
              children: (
                <Flex align="center" gap={8} wrap>
                  {creds ? <Text code copyable>{creds.password}</Text> : <Text code>{MASK}</Text>}
                  <Button
                    icon={creds ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                    loading={busy}
                    onClick={() => void toggle()}
                    size="small"
                  >
                    {creds ? 'Hide' : 'Reveal'}
                  </Button>
                </Flex>
              ),
              key: 'password',
              label: 'Password',
            },
            {
              children: creds ? <Text code copyable style={{ wordBreak: 'break-all' }}>{creds.url}</Text> : hidden,
              key: 'url',
              label: 'URL',
            },
          ]}
        />
        {revealError && <Alert showIcon style={{ marginTop: 16 }} title={revealError} type="error" />}
      </Card>
      <Card title="Compose snippet" variant="outlined">
        {creds ? (
          <Typography.Paragraph copyable={{ text: snippet(creds.url) }} style={{ marginBottom: 0 }}>
            <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{snippet(creds.url)}</pre>
          </Typography.Paragraph>
        ) : (
          hidden
        )}
      </Card>
    </Flex>
  );
}

function LogsTab({ id }: { id: number }) {
  const { data, error, isLoading, mutate } = useSWR(`/api/databases/${id}/logs`, () => api.databaseLogs(id, 200));
  return (
    <Card
      extra={
        <Button icon={<ReloadOutlined />} loading={isLoading} onClick={() => void mutate()}>
          Refresh
        </Button>
      }
      title="Last 200 lines"
      variant="outlined"
    >
      {error ? (
        <Alert showIcon title={failure(error, 'Could not read the logs')} type="error" />
      ) : (
        <pre
          style={{
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            fontSize: 12,
            margin: 0,
            maxHeight: 480,
            overflow: 'auto',
            whiteSpace: 'pre-wrap',
          }}
        >
          {data?.logs || (isLoading ? 'Loading…' : 'No log lines yet.')}
        </pre>
      )}
    </Card>
  );
}

function SettingsTab({ db, onDeleted, onRenamed }: { db: Database; onDeleted: () => void; onRenamed: () => void }) {
  const { message } = App.useApp();
  const [name, setName] = useState(db.name);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [error, setError] = useState<string>();

  const rename = async () => {
    setSaving(true);
    setError(undefined);
    try {
      await api.renameDatabase(db.id, name.trim());
      void message.success('Renamed');
      onRenamed();
    } catch (reason: unknown) {
      setError(failure(reason, 'Could not rename the Database'));
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    setDeleteBusy(true);
    setError(undefined);
    try {
      await api.deleteDatabase(db.id, db.name);
      onDeleted();
    } catch (reason: unknown) {
      setError(failure(reason, 'Could not delete the Database'));
      setDeleting(false);
    } finally {
      setDeleteBusy(false);
    }
  };

  return (
    <Flex gap={16} vertical>
      {error && <Alert showIcon title={error} type="error" />}
      <Card title="Name" variant="outlined">
        <Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
          Renaming changes the readable host only. The Database is not restarted, and the stable host stays {db.host}.
        </Text>
        <Flex gap={8}>
          <Input aria-label="Name" onChange={(event) => setName(event.target.value)} value={name} />
          <Button
            disabled={!name.trim() || name.trim() === db.name}
            loading={saving}
            onClick={() => void rename()}
            type="primary"
          >
            Rename
          </Button>
        </Flex>
      </Card>
      <Card title="Delete" variant="outlined">
        <Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
          Removes the container and its data volume. There are no backups yet, so the data is gone for good.
        </Text>
        <Button danger onClick={() => setDeleting(true)}>
          Delete database
        </Button>
      </Card>
      <DangerModal
        confirmWord={db.name}
        description="Removes the container and the data volume."
        loading={deleteBusy}
        okText="Delete database"
        onCancel={() => setDeleting(false)}
        onConfirm={() => void remove()}
        open={deleting}
        title={`Delete ${db.name}?`}
      />
    </Flex>
  );
}
