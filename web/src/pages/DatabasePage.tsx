import { EyeInvisibleOutlined, EyeOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Checkbox, Descriptions, Flex, Input, Modal, Select, Tabs, Tag, Tooltip, Typography } from 'antd';
import { useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router';
import useSWR, { useSWRConfig } from 'swr';

import { paths } from '../app/paths';
import { ChangeVersionModal } from '../components/ChangeVersionModal';
import { DangerModal } from '../components/DangerModal';
import { DatabaseRecreateModal } from '../components/DatabaseRecreateModal';
import { DatabaseVersionChangesCard } from '../components/DatabaseVersionChangesCard';
import { PageHeader } from '../components/primitives';
import { api } from '../data/api';
import type { Database, DatabaseAction, DatabaseCredentials } from '../data/types';
import { DatabaseBackupsTab } from './DatabaseBackupsTab';
import { DatabaseDatabasesTab } from './DatabaseDatabasesTab';
import { DatabaseUsersTab } from './DatabaseUsersTab';
import { DatabaseQueryTab } from './DatabaseQueryTab';
import { DatabaseRedisTab } from './DatabaseRedisTab';
import { DirectAccessCard } from './DirectAccessCard';
import { canBackUp, canRepair, credentialsDatabase, jobNotice, pollInterval, snippet, statusMeta } from './databases-view';

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
  const { mutate: refreshKey } = useSWRConfig();
  const { data, error, isLoading, mutate } = useSWR(key, () => api.getDatabase(databaseId), {
    refreshInterval: (row) => pollInterval(row ? [row] : undefined),
  });
  const [params, setParams] = useSearchParams();
  const [busy, setBusy] = useState<DatabaseAction>();
  const [actionError, setActionError] = useState<string>();
  const [updating, setUpdating] = useState(false);
  const [updateBusy, setUpdateBusy] = useState(false);
  const [changing, setChanging] = useState(false);
  const [recreating, setRecreating] = useState(false);

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

  // Back up first is on by default, and only offered where a Backup can run.
  const [backupFirst, setBackupFirst] = useState(true);
  const sql = data !== undefined && data.engine !== 'redis';
  const canBackupFirst = data !== undefined && canBackUp(data);
  const updateImage = async () => {
    if (!data) return;
    const withBackup = sql && canBackupFirst && backupFirst;
    setUpdateBusy(true);
    setActionError(undefined);
    try {
      await api.updateDatabaseImage(databaseId, withBackup);
      void message.success(withBackup ? `Backing up ${data.name}, then updating its image` : `Updated the image of ${data.name}`);
      setUpdating(false);
      await mutate();
    } catch (reason: unknown) {
      setActionError(failure(reason, 'Something went wrong'));
      setUpdating(false);
      void mutate();
    } finally {
      setUpdateBusy(false);
    }
  };

  const openTab = (tab: string) => setParams({ tab }, { replace: true });
  const name = data?.name ?? 'Database';
  const jobRunning = Boolean(data?.job);
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
          data && !data.deleted && (
            <Flex gap={8} wrap>
              {wantsRunning ? (
                <Button disabled={jobRunning} loading={busy === 'stop'} onClick={() => void act('stop', `Stopped ${name}`)}>
                  Stop
                </Button>
              ) : (
                <Button disabled={jobRunning} loading={busy === 'start'} onClick={() => void act('start', `Started ${name}`)} type="primary">
                  Start
                </Button>
              )}
              <Button
                disabled={!wantsRunning || jobRunning}
                loading={busy === 'restart'}
                onClick={() => void act('restart', `Restarted ${name}`)}
              >
                Restart
              </Button>
              <Tooltip title="Pulls the tag again and applies this panel's container settings. Recreates the container on the same volume only if either changed; a stopped Database stays stopped.">
                <Button
                  disabled={jobRunning}
                  onClick={() => {
                    setBackupFirst(true);
                    setUpdating(true);
                  }}
                >
                  Update image
                </Button>
              </Tooltip>
              {data.upgrades.length > 0 && (
                <Tooltip title={data.status === 'running' ? undefined : 'Start the Database first'}>
                  <Button disabled={jobRunning || data.status !== 'running'} onClick={() => setChanging(true)}>
                    Change version
                  </Button>
                </Tooltip>
              )}
              {canRepair(data) && (
                <Button
                  disabled={jobRunning}
                  loading={busy === 'repair'}
                  onClick={() => void act('repair', `Repaired ${name}`)}
                  type={wantsRunning ? 'primary' : 'default'}
                >
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
      {data?.deleted && (
        <Alert
          showIcon
          style={{ marginBottom: 16 }}
          title="This Database was deleted. Its backups are kept until you delete them."
          type="info"
        />
      )}
      {data && jobRunning && <Alert showIcon style={{ marginBottom: 16 }} title={jobNotice(data.job)} type="info" />}
      {data?.status === 'volume_missing' && (
        <Alert
          action={
            data.engine !== 'redis' && (
              <Button danger onClick={() => setRecreating(true)}>
                Recreate and restore…
              </Button>
            )
          }
          description={
            data.engine === 'redis'
              ? 'Nothing was recreated, because an empty new volume would look like the data had never been lost. Redis keeps no Backups: delete this Database and create it again.'
              : 'Nothing was recreated, because an empty new volume would look like the data had never been lost. Recreate it from its Backups, with the same hosts and password, or delete it.'
          }
          showIcon
          style={{ marginBottom: 16 }}
          title="The data volume is gone"
          type="error"
        />
      )}
      {data && data.engine !== 'redis' && (
        <DatabaseRecreateModal
          db={data}
          onClose={() => {
            setRecreating(false);
            void mutate();
          }}
          open={recreating}
        />
      )}
      {data?.settingsOutdated && (
        <Alert
          action={
            <Button
              disabled={jobRunning}
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

      {data?.deleted && <DatabaseBackupsTab db={data} />}
      {data && !data.deleted && (
        <Tabs
          activeKey={tabKeys(data).includes(params.get('tab') ?? '') ? (params.get('tab') ?? 'connection') : 'connection'}
          destroyOnHidden
          onChange={openTab}
          items={[
            { children: <ConnectionTab db={data} />, key: 'connection', label: 'Connection' },
            ...(sql
              ? [
                  { children: <DatabaseDatabasesTab db={data} />, key: 'databases', label: 'Databases' },
                  { children: <DatabaseUsersTab db={data} />, key: 'users', label: 'Users' },
                ]
              : []),
            ...(sql ? [{ children: <DatabaseQueryTab db={data} />, key: 'query', label: 'Query' }] : []),
            ...(data.engine === 'redis' ? [{ children: <DatabaseRedisTab db={data} />, key: 'console', label: 'Console' }] : []),
            ...(sql ? [{ children: <DatabaseBackupsTab db={data} onOpenUsers={() => openTab('users')} />, key: 'backups', label: 'Backups' }] : []),
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
      {changing && data && (
        <ChangeVersionModal
          db={data}
          onClose={() => setChanging(false)}
          onStarted={() => {
            setChanging(false);
            void mutate();
            // The Settings tab's card polls only while it shows a running row.
            void refreshKey(`/api/databases/${data.id}/version-changes`);
          }}
        />
      )}
      <Modal
        confirmLoading={updateBusy}
        destroyOnHidden
        okText="Update image"
        onCancel={() => setUpdating(false)}
        onOk={() => void updateImage()}
        open={updating}
        title="Update image"
      >
        <Flex gap={12} vertical>
          <Text>
            Pulls the tag again and applies this panel's container settings. Recreates the container on the same volume
            only if either changed; a stopped Database stays stopped.
          </Text>
          {sql && (
            <div>
              <Checkbox
                checked={canBackupFirst && backupFirst}
                disabled={!canBackupFirst}
                onChange={(event) => setBackupFirst(event.target.checked)}
              >
                Back up first
              </Checkbox>
              {!canBackupFirst && (
                <Text style={{ display: 'block', marginTop: 4 }} type="secondary">
                  Start the Database to back up first
                </Text>
              )}
            </div>
          )}
        </Flex>
      </Modal>
    </>
  );
}

/** The tab keys a Database shows: SQL engines add Databases and Users. */
function tabKeys(db: Database): string[] {
  const sqlEngine = db.engine !== 'redis';
  return ['connection', ...(sqlEngine ? ['databases', 'users', 'query', 'backups'] : ['console']), 'logs', 'settings'];
}

function ConnectionTab({ db }: { db: Database }) {
  const { message } = App.useApp();
  const { mutate: refresh } = useSWRConfig();
  const [creds, setCreds] = useState<DatabaseCredentials>();
  const [busy, setBusy] = useState(false);
  const [revealError, setRevealError] = useState<string>();
  // '' is the superuser; otherwise the name of a managed user.
  const [login, setLogin] = useState('');
  const [resetting, setResetting] = useState(false);
  const [resetBusy, setResetBusy] = useState(false);
  const [resetError, setResetError] = useState<string>();
  const sqlEngine = db.engine !== 'redis';
  const users = useSWR(sqlEngine ? `/api/databases/${db.id}/users` : null, () => api.listDatabaseUsers(db.id), { shouldRetryOnError: false });
  const managed = (users.data?.users ?? []).filter((row) => row.managed);
  const picked = managed.find((row) => row.name === login);
  const shownUser = picked ? picked.name : db.user;

  const reset = async () => {
    setResetBusy(true);
    setResetError(undefined);
    try {
      await api.resetSuperuserPassword(db.id, db.name);
      void message.success(`Resetting the superuser password of ${db.name}`);
      setCreds(undefined);
      setResetting(false);
      void refresh(`/api/databases/${db.id}`);
    } catch (reason: unknown) {
      setResetError(failure(reason, 'Could not reset the password'));
    } finally {
      setResetBusy(false);
    }
  };

  // The password is fetched only on Reveal: every read is audited.
  const toggle = async () => {
    if (creds) {
      setCreds(undefined);
      return;
    }
    setBusy(true);
    setRevealError(undefined);
    try {
      setCreds(
        picked
          ? await api.databaseCredentials(db.id, picked.name, credentialsDatabase(picked, db.defaultDatabase))
          : await api.databaseCredentials(db.id, undefined, undefined),
      );
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
            {
              children: sqlEngine ? (
                <Flex align="center" gap={8} wrap>
                  <Select
                    aria-label="User"
                    onChange={(value: string) => {
                      setLogin(value);
                      setCreds(undefined);
                      setRevealError(undefined);
                    }}
                    options={[
                      { label: `${db.user} (superuser)`, value: '' },
                      ...managed.map((row) => ({ label: row.name, value: row.name })),
                    ]}
                    style={{ minWidth: 200 }}
                    value={login}
                  />
                  <Text code copyable>{shownUser}</Text>
                </Flex>
              ) : (
                <Text code copyable>{db.user}</Text>
              ),
              key: 'user',
              label: 'User',
            },
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
      <Card title="Reset superuser password" variant="outlined">
        <Text style={{ display: 'block', marginBottom: 12 }} type="secondary">
          Generates a new password for {db.user} and recreates the container with it. Apps using the superuser password
          stop connecting until given the new one.
        </Text>
        {resetError && <Alert showIcon style={{ marginBottom: 12 }} title={resetError} type="error" />}
        <Button
          danger
          disabled={Boolean(db.job) || !(db.status === 'running' || db.status === 'starting' || db.status === 'unhealthy')}
          onClick={() => {
            setResetError(undefined);
            setResetting(true);
          }}
        >
          Reset superuser password
        </Button>
      </Card>
      <DangerModal
        confirmWord={db.name}
        description={
          <>
            Apps using the superuser password stop connecting until given the new one. The Database restarts once, briefly,
            and its Query and Backups wait until it is back.
          </>
        }
        loading={resetBusy}
        okText="Reset password"
        onCancel={() => setResetting(false)}
        onConfirm={() => void reset()}
        open={resetting}
        title="Reset superuser password?"
      />
      <DirectAccessCard db={db} />
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
  const [deleteBackups, setDeleteBackups] = useState(false);
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
      await api.deleteDatabase(db.id, db.name, deleteBackups);
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
      <DatabaseVersionChangesCard db={db} />
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
          {db.engine === 'redis'
            ? 'Removes the container and its data volume. A Redis Database has no backups, so the data is gone for good.'
            : 'Removes the container and its data volume. Its backups are kept, and listed here, unless you also delete them.'}
        </Text>
        <Button
          danger
          disabled={Boolean(db.job)}
          onClick={() => {
            setDeleteBackups(false);
            setDeleting(true);
          }}
        >
          Delete database
        </Button>
      </Card>
      <DangerModal
        confirmWord={db.name}
        consequences={
          db.engine === 'redis' ? undefined : (
            <Checkbox checked={deleteBackups} onChange={(event) => setDeleteBackups(event.target.checked)}>
              Also delete its backups
            </Checkbox>
          )
        }
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
