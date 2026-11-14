import { CloudUploadOutlined, DownloadOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Checkbox, Flex, Modal, Popconfirm, Radio, Table, Tag, Tooltip, Typography, Upload } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { DangerModal } from '../components/DangerModal';
import { RelativeTime } from '../components/primitives';
import { api } from '../data/api';
import type { BackupScope, Database, DatabaseBackup, DatabaseRestore, JobStatus } from '../data/types';
import { backupsPollInterval, canBackUp, formatBytes, restoreWarning, scopeLabel, triggerLabel } from './databases-view';

const { Text } = Typography;

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

type RestoreTarget = { backup: DatabaseBackup; kind: 'backup' } | { file: File; kind: 'upload' };

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
  const [mode, setMode] = useState<'all' | 'selected'>('all');
  const [picked, setPicked] = useState<string[]>([]);
  const [target, setTarget] = useState<RestoreTarget>();
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const logical = useSWR(backingUp && mode === 'selected' ? `${key}/logical` : null, () => api.databaseLogicalDatabases(db.id));

  const reading = canBackUp(db) && !db.job;
  const reason = db.job ? 'Another job is running on this Database.' : 'Start the Database first.';

  const openBackup = () => {
    setMode('all');
    setPicked([]);
    setBackingUp(true);
  };

  const backUp = async () => {
    const scope: BackupScope = mode === 'all' ? { all: true } : { databases: picked };
    setBusy(true);
    setActionError(undefined);
    try {
      await api.startDatabaseBackup(db.id, scope);
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
    if (!target) return;
    setBusy(true);
    setActionError(undefined);
    try {
      if (target.kind === 'backup') {
        await api.restoreDatabase(db.id, target.backup.id, db.name);
      } else {
        await api.restoreDatabaseUpload(db.id, target.file, db.name);
      }
      void message.success('Restore started');
      await mutate();
    } catch (reasonError: unknown) {
      setActionError(failure(reasonError, 'Could not start the restore'));
    } finally {
      setBusy(false);
      setTarget(undefined);
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

  const columns: TableColumnsType<DatabaseBackup> = [
    { key: 'started', render: (_, row) => <RelativeTime value={row.startedAt} />, title: 'Started' },
    { key: 'trigger', render: (_, row) => <Tag>{triggerLabel(row.trigger)}</Tag>, title: 'Trigger' },
    { key: 'scope', render: (_, row) => scopeLabel(row.scope), title: 'Scope' },
    { key: 'size', render: (_, row) => (row.status === 'succeeded' ? formatBytes(row.size) : '—'), title: 'Size' },
    { key: 'status', render: (_, row) => <StatusTag status={row.status} />, title: 'Status' },
    {
      key: 'actions',
      render: (_, row) => (
        <Flex gap={4} wrap>
          {row.status === 'succeeded' && (
            <Button href={api.databaseBackupUrl(db.id, row.id)} icon={<DownloadOutlined />} size="small" type="link">
              Download
            </Button>
          )}
          {row.status === 'succeeded' && !db.deleted && (
            <Tooltip title={reading ? undefined : reason}>
              <Button
                disabled={!reading}
                onClick={() => setTarget({ backup: row, kind: 'backup' })}
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

  const warning = target ? restoreWarning(target.kind === 'upload' ? 'upload' : target.backup.scope) : [];

  return (
    <Flex gap={16} vertical>
      {error && <Alert showIcon title={failure(error, 'Could not load the backups')} type="error" />}
      {actionError && (
        <Alert closable={{ onClose: () => setActionError(undefined) }} showIcon title={actionError} type="error" />
      )}
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
                    setTarget({ file, kind: 'upload' });
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
        okButtonProps={{ disabled: mode === 'selected' && picked.length === 0 }}
        okText="Back up"
        onCancel={() => setBackingUp(false)}
        onOk={() => void backUp()}
        open={backingUp}
        title="Back up now"
      >
        <Flex gap={12} vertical>
          <Radio.Group onChange={(event) => setMode(event.target.value as 'all' | 'selected')} value={mode}>
            <Radio value="all">Whole instance</Radio>
            <Radio value="selected">Selected databases</Radio>
          </Radio.Group>
          {mode === 'selected' &&
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
          {mode === 'selected' && logical.data?.databases.length === 0 && (
            <Text type="secondary">This Database has no databases of its own yet.</Text>
          )}
        </Flex>
      </Modal>

      <DangerModal
        confirmWord={db.name}
        consequences={
          <>
            {target?.kind === 'upload' && <Text code>{target.file.name}</Text>}
            <p style={{ marginBottom: 0 }}>{warning[1]}</p>
          </>
        }
        description={warning[0]}
        loading={busy}
        okText="Restore"
        onCancel={() => setTarget(undefined)}
        onConfirm={() => void restore()}
        open={target !== undefined}
        title={`Restore ${db.name}?`}
      />
    </Flex>
  );
}
