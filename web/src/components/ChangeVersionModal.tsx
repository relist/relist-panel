import { App, Checkbox, Flex, Form, Modal, Select, Typography } from 'antd';
import { useState } from 'react';

import { api } from '../data/api';
import type { Database } from '../data/types';
import { engineLabel } from '../pages/databases-view';

const { Text } = Typography;

/** What a change to `to` does, in the words of the engine's method (ADR-0047). */
function explain(db: Database, to: string): string {
  const label = engineLabel(db.engine);
  switch (db.engine) {
    case 'postgres':
      return (
        `Backs up every database and the roles, then moves the data to ${label} ${to} on a new volume. ` +
        'Apps keep the same host, user and password. Clients are disconnected from the start, and the Database is ' +
        `unavailable until it ends. If any step fails, ${db.version} comes back as it was. The old data is kept as ` +
        'Previous version data until you delete it.'
      );
    case 'mariadb':
      return `Changes the image in place; MariaDB upgrades its data on the first start of ${to}. There is no going back to ${db.version} once it starts.`;
    default:
      return `Changes the image in place. ${label} ${to} rewrites the data file in a format ${db.version} cannot read, so there is no going back.`;
  }
}

/** Change version: pick a higher version of the engine and start the job. */
export function ChangeVersionModal({ db, onClose, onStarted }: { db: Database; onClose: () => void; onStarted: () => void }) {
  const { message } = App.useApp();
  const [to, setTo] = useState(db.upgrades[db.upgrades.length - 1] ?? '');
  const [backupFirst, setBackupFirst] = useState(true);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string>();

  const submit = async () => {
    setBusy(true);
    setFailure(undefined);
    try {
      await api.changeDatabaseVersion(db.id, to, db.engine === 'mariadb' && backupFirst);
      void message.success(`Changing ${db.name} to ${to}`);
      onStarted();
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not change the version');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      confirmLoading={busy}
      destroyOnHidden
      okText={`Change to ${to}`}
      onCancel={onClose}
      onOk={() => void submit()}
      open
      title="Change version"
    >
      <Flex gap={12} vertical>
        <Form layout="vertical">
          <Form.Item label="Version" style={{ marginBottom: 0 }}>
            <Select
              aria-label="Version"
              onChange={setTo}
              options={db.upgrades.map((tag) => ({ label: tag, value: tag }))}
              style={{ maxWidth: 200 }}
              value={to}
            />
          </Form.Item>
        </Form>
        <Text>{explain(db, to)}</Text>
        {db.engine === 'mariadb' && (
          <Checkbox checked={backupFirst} onChange={(event) => setBackupFirst(event.target.checked)}>
            Back up first
          </Checkbox>
        )}
        {failure && <Text type="danger">{failure}</Text>}
      </Flex>
    </Modal>
  );
}
