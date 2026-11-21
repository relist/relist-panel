import { CloudUploadOutlined, DownloadOutlined, PushpinFilled, PushpinOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Checkbox, Flex, Input, Modal, Popconfirm, Radio, Select, Table, Tag, Tooltip, Typography, Upload } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useState } from 'react';
import useSWR from 'swr';

import { DangerModal } from '../components/DangerModal';
import { DatabaseScheduleCard } from '../components/DatabaseScheduleCard';
import { RelativeTime } from '../components/primitives';
import { api } from '../data/api';
import type { BackupRequest, Database, DatabaseBackup, DatabaseRestore, JobStatus } from '../data/types';
import { backupsPollInterval, canBackUp, formatBytes, restoreWarning, triggerLabel, validTargetName } from './databases-view';

const { Text } = Typography;

const REMOTE_ONLY = 'Rotation removed the file from this server. Download it from the Backup destination.';
const NOT_RESTORABLE = 'This Backup holds several databases (an earlier format); download it and restore by hand.';

const STATUS_TAG: Record<JobStatus, { color: string; label: string }> = {
  failed: { color: 'error', label: 'Failed' },
  running: { color: 'processing', label: 'Running' },
  succeeded: { color: 'success', label: 'Succeeded' },
};

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

function StatusTag({ status }: { status: JobStatus }) {
  const meta = STATUS_TAG[status] ?? { color: 'default', label: status };
  return <Tag color={meta.color}>{meta.label}</Tag>;
}

const monospace = {
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  fontSize: 12,
  margin: 0,
  maxHeight: 320,
  overflow: 'auto',
  whiteSpace: 'pre-wrap',
} as const;

/** The error and the log of a failed Backup or restore. */
function FailureDetail({ error, log }: { error: string; log: string }) {
  return (
    <pre style={monospace}>
      {error}
      {log ? `\n\n${log}` : ''}
    </pre>
  );
}

type RestoreSource = { backup: DatabaseBackup; kind: 'backup' } | { file: File; kind: 'upload' };

/**
 * A Database's Backups: back up now, download, restore (from a Backup or an
 * uploaded file) and delete. SQL engines only. A deleted Database (a
 * tombstone) keeps only this tab, for downloading and deleting its files.
 */
export function DatabaseBackupsTab({ db }: { db: Database }) {
  const { message } = App.useApp();
  const key = `/api/databases/${db.id}/backups`;
  const { data, error, mutate } = useSWR(
    key,
    async () => ({ backups: await api.listDatabaseBackups(db.id), restores: await api.listDatabaseRestores(db.id) }),
    { refreshInterval: (rows) => backupsPollInterval(db, rows?.backups, rows?.restores) },
  );
  const [backingUp, setBackingUp] = useState(false);
  const [scopeMode, setScopeMode] = useState<'all' | 'selected'>('all');
  const [picked, setPicked] = useState<string[]>([]);
  const [source, setSource] = useState<RestoreSource>();
  // Where the restore goes. undefined until the Admin picks, or until a
  // Backup's default is known.
  const [targetMode, setTargetMode] = useState<'replace' | 'new'>();
  const [replaceName, setReplaceName] = useState('');
  const [newName, setNewName] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const logical = useSWR(source || (backingUp && scopeMode === 'selected') ? `${key}/logical` : null, () => api.databaseLogicalDatabases(db.id));

  const existing = logical.data?.databases ?? [];

  useEffect(() => {
    setTargetMode(undefined);
    setReplaceName('');
    setNewName('');
  }, [source]);

  // A Backup restores into its own database when it still exists, and into a
  // new database of the same name when it is gone. An upload has no default.
  useEffect(() => {
    if (source?.kind !== 'backup' || !logical.data || targetMode) return;
    const name = source.backup.database;
    if (logical.data.databases.includes(name)) {
      setTargetMode('replace');
      setReplaceName(name);
    } else {
      setTargetMode('new');
      setNewName(name);
    }
  }, [source, logical.data, targetMode]);

  const reading = canBackUp(db) && !db.job;
  const reason = db.job ? 'Another job is running on this Database.' : 'Start the Database first.';

  const openBackup = () => {
    setScopeMode('all');
    setPicked([]);
    setBackingUp(true);
  };

  const backUp = async () => {
    const request: BackupRequest = scopeMode === 'all' ? { all: true } : { databases: picked };
    setBusy(true);
    setActionError(undefined);
    try {
      await api.startDatabaseBackup(db.id, request);
      void message.success('Backup started');
      setBackingUp(false);
      await mutate();
    } catch (reasonError: unknown) {
      setActionError(failure(reasonError, 'Could not start the backup'));
      setBackingUp(false);
    } finally {
      setBusy(false);
    }
  };

  const restore = async () => {
    if (!source || !resolvedTarget) return;
    setBusy(true);
    setActionError(undefined);
    try {
      if (source.kind === 'backup') {
        await api.restoreDatabase(db.id, source.backup.id, resolvedTarget, db.name);
      } else {
        await api.restoreDatabaseUpload(db.id, source.file, resolvedTarget, db.name);
      }
      void message.success('Restore started');
      await mutate();
    } catch (reasonError: unknown) {
      setActionError(failure(reasonError, 'Could not start the restore'));
    } finally {
      setBusy(false);
      setSource(undefined);
    }
  };

  const remove = async (backup: DatabaseBackup) => {
    setActionError(undefined);
    try {
      await api.deleteDatabaseBackup(db.id, backup.id);
      void message.success('Backup deleted');
      await mutate();
    } catch (reasonError: unknown) {
      setActionError(failure(reasonError, 'Could not delete the backup'));
    }
  };

  const pin = async (backup: DatabaseBackup) => {
    setActionError(undefined);
    try {
      await api.pinDatabaseBackup(db.id, backup.id, !backup.pinned);
      await mutate();
    } catch (reasonError: unknown) {
      setActionError(failure(reasonError, 'Could not change the pin'));
    }
  };

  const columns: TableColumnsType<DatabaseBackup> = [
    {
      key: 'database',
      render: (_, row) => (row.database ? <Text code>{row.database}</Text> : <Tag>Several databases</Tag>),
      title: 'Database',
    },
    { key: 'started', render: (_, row) => <RelativeTime value={row.startedAt} />, title: 'Started' },
    {
      key: 'trigger',
      render: (_, row) => (
        <Flex gap={4} wrap>
          <Tag>{triggerLabel(row.trigger)}</Tag>
          {row.localRemoved && <Tag color="warning">Remote only</Tag>}
        </Flex>
      ),
      title: 'Trigger',
    },
    { key: 'size', render: (_, row) => (row.status === 'succeeded' ? formatBytes(row.size) : '—'), title: 'Size' },
    { key: 'status', render: (_, row) => <StatusTag status={row.status} />, title: 'Status' },
    {
      key: 'actions',
      render: (_, row) => (
        <Flex gap={4} wrap>
          {row.trigger === 'scheduled' && row.status !== 'running' && (
            <Tooltip title={row.pinned ? 'Pinned: Rotation keeps it' : 'Pin: Rotation never deletes it'}>
              <Button
                aria-label={row.pinned ? 'Unpin' : 'Pin'}
                icon={row.pinned ? <PushpinFilled /> : <PushpinOutlined />}
                onClick={() => void pin(row)}
                size="small"
                type="link"
              />
            </Tooltip>
          )}
          {row.status === 'succeeded' && row.localRemoved && (
            <Tooltip title={REMOTE_ONLY}>
              <Button disabled icon={<DownloadOutlined />} size="small" type="link">
                Download
              </Button>
            </Tooltip>
          )}
          {row.status === 'succeeded' && !row.localRemoved && (
            <Button href={api.databaseBackupUrl(db.id, row.id)} icon={<DownloadOutlined />} size="small" type="link">
              Download
            </Button>
          )}
          {row.status === 'succeeded' && !db.deleted && (
            <Tooltip title={row.localRemoved ? REMOTE_ONLY : !row.database ? NOT_RESTORABLE : reading ? undefined : reason}>
              <Button
                disabled={!reading || !row.database || row.localRemoved}
                onClick={() => setSource({ backup: row, kind: 'backup' })}
                size="small"
                type="link"
              >
                Restore
              </Button>
            </Tooltip>
          )}
          <Popconfirm
            description="The file is removed from the panel's disk."
            disabled={row.status === 'running'}
            okButtonProps={{ danger: true }}
            okText="Delete"
            onConfirm={() => void remove(row)}
            title="Delete this backup?"
          >
            <Button danger disabled={row.status === 'running'} size="small" type="link">
              Delete
            </Button>
          </Popconfirm>
        </Flex>
      ),
      title: '',
    },
  ];

  const backupById = new Map((data?.backups ?? []).map((row) => [row.id, row]));
  const restoreColumns: TableColumnsType<DatabaseRestore> = [
    { key: 'started', render: (_, row) => <RelativeTime value={row.startedAt} />, title: 'Started' },
    {
      key: 'source',
      render: (_, row) => (row.source === 'upload' ? `Uploaded ${row.uploadName}` : `Backup #${row.backupId}`),
      title: 'Source',
    },
    { key: 'target', render: (_, row) => (row.target ? <Text code>{row.target}</Text> : '—'), title: 'Into' },
    { key: 'status', render: (_, row) => <StatusTag status={row.status} />, title: 'Status' },
    {
      key: 'pre',
      render: (_, row) => {
        if (row.preBackupId == null) return <Text type="secondary">—</Text>;
        const pre = backupById.get(row.preBackupId);
        return pre?.status === 'succeeded' ? (
          <a href={api.databaseBackupUrl(db.id, pre.id)}>Backup #{pre.id}</a>
        ) : (
          `Backup #${row.preBackupId}`
        );
      },
      title: 'Backup taken before',
    },
  ];

  const replacing = targetMode === 'replace';
  const resolvedTarget = replacing ? replaceName : targetMode === 'new' && validTargetName(newName) ? newName : '';
  const warning = resolvedTarget ? restoreWarning(resolvedTarget, replacing) : '';

  return (
    <Flex gap={16} vertical>
      {error && <Alert showIcon title={failure(error, 'Could not load the backups')} type="error" />}
      {actionError && (
        <Alert closable={{ onClose: () => setActionError(undefined) }} showIcon title={actionError} type="error" />
      )}
      {!db.deleted && db.engine !== 'redis' && <DatabaseScheduleCard db={db} />}
      <Card
        extra={
          !db.deleted && (
            <Flex gap={8} wrap>
              <Tooltip title={reading ? undefined : reason}>
                <Button disabled={!reading} onClick={openBackup} type="primary">
                  Back up now
                </Button>
              </Tooltip>
              <Tooltip title={reading ? undefined : reason}>
                <Upload
                  accept=".sql,.gz"
                  beforeUpload={(file) => {
                    setSource({ file, kind: 'upload' });
                    return false;
                  }}
                  disabled={!reading}
                  maxCount={1}
                  showUploadList={false}
                >
                  <Button disabled={!reading} icon={<CloudUploadOutlined />}>
                    Restore from file…
                  </Button>
                </Upload>
              </Tooltip>
              <Text style={{ flexBasis: '100%', textAlign: 'right' }} type="secondary">
                A dump of one database: pg_dump &lt;db&gt; or mysqldump &lt;db&gt;.
              </Text>
            </Flex>
          )
        }
        title="Backups"
        variant="outlined"
      >
        <Table<DatabaseBackup>
          columns={columns}
          dataSource={data?.backups ?? []}
          expandable={{
            expandedRowRender: (row) => <FailureDetail error={row.error} log={row.log} />,
            rowExpandable: (row) => row.status === 'failed',
          }}
          loading={!data && !error}
          locale={{ emptyText: 'No backups yet.' }}
          pagination={false}
          rowKey="id"
          size="small"
        />
      </Card>
      {!db.deleted && (
        <Card title="Restores" variant="outlined">
          <Table<DatabaseRestore>
            columns={restoreColumns}
            dataSource={data?.restores ?? []}
            expandable={{
              expandedRowRender: (row) => <FailureDetail error={row.error} log={row.log} />,
              rowExpandable: (row) => row.status === 'failed',
            }}
            locale={{ emptyText: 'No restores yet.' }}
            pagination={false}
            rowKey="id"
            size="small"
          />
        </Card>
      )}

      <Modal
        confirmLoading={busy}
        destroyOnHidden
        okButtonProps={{ disabled: scopeMode === 'selected' && picked.length === 0 }}
        okText="Back up"
        onCancel={() => setBackingUp(false)}
        onOk={() => void backUp()}
        open={backingUp}
        title="Back up now"
      >
        <Flex gap={12} vertical>
          <Radio.Group onChange={(event) => setScopeMode(event.target.value as 'all' | 'selected')} value={scopeMode}>
            <Radio value="all">All databases</Radio>
            <Radio value="selected">Choose databases</Radio>
          </Radio.Group>
          <Text type="secondary">One file per database.</Text>
          {scopeMode === 'selected' &&
            (logical.error ? (
              <Alert showIcon title={failure(logical.error, 'Could not list the databases')} type="error" />
            ) : (
              <Checkbox.Group
                onChange={(values) => setPicked(values as string[])}
                options={(logical.data?.databases ?? []).map((name) => ({ label: name, value: name }))}
                style={{ display: 'flex', flexDirection: 'column', gap: 4 }}
                value={picked}
              />
            ))}
          {scopeMode === 'selected' && logical.data?.databases.length === 0 && (
            <Text type="secondary">This Database has no databases of its own yet.</Text>
          )}
        </Flex>
      </Modal>

      <DangerModal
        confirmWord={db.name}
        consequences={
          <Flex gap={12} vertical>
            {source?.kind === 'upload' && <Text code>{source.file.name}</Text>}
            <Radio.Group onChange={(event) => setTargetMode(event.target.value as 'replace' | 'new')} value={targetMode}>
              <Flex gap={4} vertical>
                <Radio value="replace">Replace an existing database</Radio>
                <Radio value="new">Restore into a new database</Radio>
              </Flex>
            </Radio.Group>
            {logical.error && <Alert showIcon title={failure(logical.error, 'Could not list the databases')} type="error" />}
            {targetMode === 'replace' && (
              <Select
                aria-label="Database to replace"
                onChange={setReplaceName}
                options={existing.map((name) => ({ label: name, value: name }))}
                placeholder="Pick a database"
                value={replaceName || undefined}
              />
            )}
            {targetMode === 'new' && (
              <Input
                aria-label="New database name"
                autoComplete="off"
                onChange={(event) => setNewName(event.target.value)}
                placeholder="Letters, digits, _ and -"
                status={newName && !validTargetName(newName) ? 'error' : undefined}
                value={newName}
              />
            )}
          </Flex>
        }
        description={warning}
        disabled={!resolvedTarget}
        loading={busy}
        okText="Restore"
        onCancel={() => setSource(undefined)}
        onConfirm={() => void restore()}
        open={source !== undefined}
        title={`Restore into ${db.name}?`}
      />
    </Flex>
  );
}
