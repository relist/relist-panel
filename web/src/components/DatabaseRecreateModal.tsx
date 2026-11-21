import { Alert, App, Checkbox, Flex, Select, Typography } from 'antd';
import dayjs from 'dayjs';
import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { Database, DatabaseBackup } from '../data/types';
import { formatBytes, statelessWarning, triggerLabel } from '../pages/databases-view';
import { DangerModal } from './DangerModal';

const { Text } = Typography;

/** The Backups a Recreate can restore, by database, newest first: succeeded, and still on disk. */
export function restorableByDatabase(backups: DatabaseBackup[]): Map<string, DatabaseBackup[]> {
  const out = new Map<string, DatabaseBackup[]>();
  const sorted = [...backups].sort((a, b) => b.startedAt.localeCompare(a.startedAt));
  for (const b of sorted) {
    if (b.status !== 'succeeded' || b.localRemoved || b.database === '') continue;
    out.set(b.database, [...(out.get(b.database) ?? []), b]);
  }
  return out;
}

function backupLabel(b: DatabaseBackup): string {
  return `${dayjs(b.startedAt).format('D MMM YYYY, HH:mm')} · ${triggerLabel(b.trigger)} · ${formatBytes(b.size)}`;
}

/**
 * Recreate after the data volume is lost (ADR-0050): a new, empty volume with
 * the Database's password, the users the panel made, and one Backup of each
 * ticked database restored as it was backed up.
 */
export function DatabaseRecreateModal({ db, onClose, open }: { db: Database; onClose: () => void; open: boolean }) {
  const { message } = App.useApp();
  const backups = useSWR(open ? `/api/databases/${db.id}/backups` : null, () => api.listDatabaseBackups(db.id));
  const byDatabase = useMemo(() => restorableByDatabase(backups.data ?? []), [backups.data]);
  // The Backup picked for each database; a database left out has none.
  const [picked, setPicked] = useState<Record<string, number | undefined>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  // Picks start as the newest of each database once per opening: a reload
  // of the Backups list while the dialog is open keeps the Admin's picks.
  const [seeded, setSeeded] = useState(false);

  useEffect(() => {
    if (!open) {
      setSeeded(false);
      return;
    }
    if (seeded || backups.data === undefined) return;
    const next: Record<string, number> = {};
    for (const [name, list] of byDatabase) next[name] = list[0].id;
    setPicked(next);
    setError(undefined);
    setSeeded(true);
  }, [open, seeded, backups.data, byDatabase]);

  const ids = Object.values(picked).filter((id): id is number => id !== undefined);

  const recreate = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await api.recreateDatabase(db.id, db.name, ids);
      void message.success('Recreate started');
      onClose();
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Could not start the Recreate');
    } finally {
      setBusy(false);
    }
  };

  return (
    <DangerModal
      confirmWord={db.name}
      consequences={
        <Flex gap={12} vertical>
          {backups.error && <Alert showIcon title="Could not list the Backups" type="error" />}
          {error && <Alert showIcon title={error} type="error" />}
          {backups.data && byDatabase.size === 0 && (
            <Text type="secondary">No Backup is on disk: the new volume starts empty.</Text>
          )}
          {[...byDatabase].map(([name, list]) => {
            const pick = list.find((b) => b.id === picked[name]);
            return (
              <Flex gap={4} key={name} vertical>
                <Flex align="center" gap={8} wrap>
                  <Checkbox
                    aria-label={`Restore ${name}`}
                    checked={picked[name] !== undefined}
                    onChange={(event) => setPicked((current) => ({ ...current, [name]: event.target.checked ? list[0].id : undefined }))}
                  >
                    <Text code>{name}</Text>
                  </Checkbox>
                  <Select
                    aria-label={`Backup of ${name}`}
                    disabled={picked[name] === undefined}
                    onChange={(id: number) => setPicked((current) => ({ ...current, [name]: id }))}
                    options={list.map((b) => ({ label: backupLabel(b), value: b.id }))}
                    style={{ flex: '1 1 240px', minWidth: 0 }}
                    value={picked[name]}
                  />
                </Flex>
                {pick && pick.asBackedUp === '' && <Text type="warning">{statelessWarning(db.engine, name)}</Text>}
              </Flex>
            );
          })}
        </Flex>
      }
      description={`Creates an empty volume and starts ${db.name} with its password, so its hosts and credentials stay the same. The users the panel made come back with their passwords, then each ticked database is restored as it was backed up, with its owner and its users' access. Users made outside the panel do not come back.`}
      disabled={backups.data === undefined}
      loading={busy}
      okText="Recreate"
      onCancel={onClose}
      onConfirm={() => void recreate()}
      open={open}
      title={`Recreate ${db.name}?`}
    />
  );
}
