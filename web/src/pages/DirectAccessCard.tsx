import { Alert, Button, Card, Checkbox, Descriptions, Flex, InputNumber, Popconfirm, Select, Statistic, Tag, Typography } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { Database, DirectAccess } from '../data/types';

const { Text } = Typography;

/** The durations the panel accepts, in minutes (ADR-0041). */
const DURATIONS = [
  { label: '15 minutes', value: 15 },
  { label: '1 hour', value: 60 },
  { label: '4 hours', value: 240 },
  { label: '8 hours', value: 480 },
];
const DEFAULT_MINUTES = 60;

/** What a Direct access reaches. A later Service page builds the same shape. */
interface Target {
  kind: 'database';
  id: number;
  port: number;
  running: boolean;
}

function targetOf(db: Database): Target {
  return { id: db.id, kind: 'database', port: db.port, running: db.status === 'running' };
}

const SCHEMES: Record<string, string> = { mariadb: 'mysql', mysql: 'mysql', postgres: 'postgres', redis: 'redis' };

/** The Connection URL's engine rules, without the password: a Redis URL has no user. */
export function directUrl(db: Database, host: string, port: number): string {
  const user = db.engine === 'redis' ? '' : `${encodeURIComponent(db.user)}@`;
  const path = db.defaultDatabase ? `/${db.defaultDatabase}` : '';
  return `${SCHEMES[db.engine] ?? db.engine}://${user}${host}:${port}${path}`;
}

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/**
 * Direct access (ADR-0041): a timed host port into the Database, for DBeaver
 * or TablePlus over an SSH tunnel. Admin only, like the whole Databases page.
 */
export function DirectAccessCard({ db }: { db: Database }) {
  const target = targetOf(db);
  const { data, error, mutate } = useSWR(`/api/direct-accesses?targetKind=${target.kind}&targetId=${target.id}`, () =>
    api.listDirectAccesses(target.kind, target.id),
  );
  const open = data?.accesses[0];
  return (
    <Card title="Direct access" variant="outlined">
      <Flex gap={16} vertical>
        {error && <Alert showIcon title={failure(error, 'Could not read the Direct access')} type="error" />}
        {open ? (
          <OpenAccess access={open} db={db} onChanged={() => void mutate()} />
        ) : (
          <OpenForm onOpened={(next) => void mutate({ accesses: [next] }, { revalidate: false })} target={target} />
        )}
      </Flex>
    </Card>
  );
}

function OpenForm({ onOpened, target }: { onOpened: (access: DirectAccess) => void; target: Target }) {
  const [minutes, setMinutes] = useState(DEFAULT_MINUTES);
  const [port, setPort] = useState<number | null>(null);
  const [allInterfaces, setAllInterfaces] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string>();

  const submit = async () => {
    setBusy(true);
    setProblem(undefined);
    try {
      onOpened(
        await api.openDirectAccess({
          allInterfaces,
          channel: 'host_port',
          hostPort: port ?? 0,
          minutes,
          targetId: target.id,
          targetKind: target.kind,
        }),
      );
    } catch (reason: unknown) {
      setProblem(failure(reason, 'Could not open the Direct access'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Flex gap={16} vertical>
      <Text type="secondary">
        Publish this Database on a host port for a desktop tool. By default it listens on 127.0.0.1, so reach it over an SSH tunnel. It
        closes by itself at the end of the duration.
      </Text>
      <Flex align="center" gap={16} wrap>
        <Flex align="center" gap={8}>
          <Text>Duration</Text>
          <Select aria-label="Duration" onChange={setMinutes} options={DURATIONS} style={{ width: 140 }} value={minutes} />
        </Flex>
        <Flex align="center" gap={8}>
          <Text>Port</Text>
          <InputNumber
            aria-label="Port"
            controls={false}
            max={65535}
            min={1024}
            onChange={setPort}
            placeholder="Any free port"
            precision={0}
            style={{ width: 140 }}
            value={port}
          />
        </Flex>
        <Checkbox checked={allInterfaces} onChange={(event) => setAllInterfaces(event.target.checked)}>
          All interfaces
        </Checkbox>
      </Flex>
      {allInterfaces && (
        <Alert
          description="Docker publishes this port past host firewalls such as UFW. Only its password protects it."
          showIcon
          title="Anyone on the internet can reach this Database"
          type="error"
        />
      )}
      {problem && <Alert showIcon title={problem} type="error" />}
      <Flex align="center" gap={12}>
        <Button disabled={!target.running} loading={busy} onClick={() => void submit()} type="primary">
          Open
        </Button>
        {!target.running && <Text type="secondary">Start the Database first</Text>}
      </Flex>
    </Flex>
  );
}

function OpenAccess({ access, db, onChanged }: { access: DirectAccess; db: Database; onChanged: () => void }) {
  const [minutes, setMinutes] = useState(DEFAULT_MINUTES);
  const [busy, setBusy] = useState<'extend' | 'close'>();
  const [problem, setProblem] = useState<string>();
  const everyone = access.bind === '0.0.0.0';
  const host = everyone ? window.location.hostname : '127.0.0.1';
  const url = directUrl(db, host, access.hostPort);

  const run = async (kind: 'extend' | 'close') => {
    setBusy(kind);
    setProblem(undefined);
    try {
      if (kind === 'extend') {
        await api.extendDirectAccess(access.id, minutes);
      } else {
        await api.closeDirectAccess(access.id);
      }
      onChanged();
    } catch (reason: unknown) {
      setProblem(failure(reason, kind === 'extend' ? 'Could not extend' : 'Could not close'));
      // It may have ended between the page load and the click.
      onChanged();
    } finally {
      setBusy(undefined);
    }
  };

  return (
    <Flex gap={16} vertical>
      {everyone && (
        <Alert
          description="Docker publishes this port past host firewalls such as UFW. Only its password protects it."
          showIcon
          title="Anyone on the internet can reach this Database"
          type="error"
        />
      )}
      <Descriptions
        bordered
        column={1}
        items={[
          { children: <Tag color="green">Open</Tag>, key: 'state', label: 'State' },
          { children: <Text code>{access.bind}</Text>, key: 'bind', label: 'Bind address' },
          { children: <Text code copyable>{String(access.hostPort)}</Text>, key: 'port', label: 'Host port' },
          {
            children: access.expiresAt ? (
              <Statistic.Timer
                onFinish={onChanged}
                styles={{ content: { fontSize: 14, fontVariantNumeric: 'tabular-nums' } }}
                type="countdown"
                value={Date.parse(access.expiresAt)}
              />
            ) : (
              <Text type="secondary">No expiry</Text>
            ),
            key: 'left',
            label: 'Time left',
          },
          {
            children: everyone ? (
              <Text code copyable>{`${host}:${access.hostPort}`}</Text>
            ) : (
              <Text code copyable>{`ssh -N -L ${access.hostPort}:127.0.0.1:${access.hostPort} <user>@${window.location.hostname}`}</Text>
            ),
            key: 'reach',
            label: everyone ? 'Server address' : 'SSH tunnel',
          },
          {
            children: (
              <Flex gap={4} vertical>
                <Text code copyable style={{ wordBreak: 'break-all' }}>{url}</Text>
                <Text type="secondary">Use the password above.</Text>
              </Flex>
            ),
            key: 'url',
            label: 'URL',
          },
        ]}
      />
      {problem && <Alert showIcon title={problem} type="error" />}
      <Flex align="center" gap={8} wrap>
        <Select aria-label="Extend by" onChange={setMinutes} options={DURATIONS} style={{ width: 140 }} value={minutes} />
        <Button loading={busy === 'extend'} onClick={() => void run('extend')}>
          Extend
        </Button>
        <Popconfirm
          description="Open connections are cut."
          okButtonProps={{ danger: true }}
          okText="Close"
          onConfirm={() => run('close')}
          title="Close this Direct access now?"
        >
          <Button danger loading={busy === 'close'}>
            Close now
          </Button>
        </Popconfirm>
      </Flex>
    </Flex>
  );
}
