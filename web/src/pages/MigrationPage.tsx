import {
  Alert,
  App,
  Button,
  Card,
  Descriptions,
  Flex,
  Form,
  Input,
  Layout,
  Progress,
  Result,
  Spin,
  Steps,
  Table,
  Tag,
  Timeline,
  Typography,
} from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, Navigate } from 'react-router';

import { paths } from '../app/paths';
import { formatBytes } from '../components/MetricChart';
import { ApiError, api } from '../data/api';
import type {
  FinalizeStep,
  HostPathCandidate,
  MigrationItem,
  MigrationItemProgress,
  MigrationLogEntry,
  MigrationProgress,
  MigrationStep,
  SourceInfo,
} from '../data/migration';

const { Paragraph, Text, Title } = Typography;

const POLL_MS = 2000;

const LOCK_STEPS: { step: MigrationStep; title: string }[] = [
  { step: 'locked', title: 'Lock and cancel queued Deployments' },
  { step: 'stopping', title: 'Stopping Composes' },
  { step: 'maintenance', title: 'Maintenance page up' },
  { step: 'ready', title: 'Ready to copy' },
];

const FINALIZE_STEPS: { step: FinalizeStep; title: string }[] = [
  { step: 'stop-traefik', title: 'Stop Traefik' },
  { step: 'move-data', title: 'Move the copied data into place' },
  { step: 'restart', title: 'Restart the panel with the migrated database' },
  { step: 'start-traefik', title: 'Start Traefik' },
  { step: 'deploy', title: 'Deploy the Composes that were running' },
  { step: 'finish', title: 'Tell the old server it is done' },
];

const LOG_COLORS: Record<MigrationLogEntry['level'], string> = { error: 'red', info: 'blue', warn: 'orange' };

/** The whole Migration as five steps, with where each one is. */
function Journey({ progress }: { progress: MigrationProgress }) {
  const { phase } = progress;
  const current = { '': 0, connected: 0, done: 4, finalizing: 3, locking: 1, paused: 2, pulling: 2 }[phase];
  const failed = phase === 'paused' || (!!progress.error && (phase === 'locking' || phase === 'finalizing'));
  const copied = progress.items.filter((i) => i.state === 'done').length;
  const lock = LOCK_STEPS.find((s) => s.step === progress.lockStep)?.title;
  const finalize = FINALIZE_STEPS.find((s) => s.step === progress.finalizeStep)?.title;
  return (
    <Steps
      aria-label="Migration steps"
      current={current}
      items={[
        { title: 'Connect' },
        { content: phase === 'locking' ? lock : undefined, title: 'Lock old server' },
        { content: progress.items.length > 0 ? `${copied} / ${progress.items.length} items` : undefined, title: 'Copy' },
        { content: phase === 'finalizing' ? finalize : undefined, title: 'Finalize' },
        { title: 'Done' },
      ]}
      size="small"
      status={phase === 'done' ? 'finish' : failed ? 'error' : 'process'}
      style={{ marginBottom: 24 }}
    />
  );
}

/** What the Migration did, newest first. */
function ActivityLog({ log }: { log: MigrationLogEntry[] }) {
  return (
    <Card size="small" style={{ marginTop: 24 }} title="Activity">
      <div style={{ maxHeight: 320, overflowY: 'auto', paddingTop: 8 }}>
        <Timeline
          aria-label="Activity log"
          items={[...log].reverse().map((entry, i) => ({
            color: LOG_COLORS[entry.level],
            content: <Text type={entry.level === 'error' ? 'danger' : undefined}>{entry.message}</Text>,
            key: `${entry.at}-${i}`,
            title: <Text type="secondary">{new Date(entry.at).toLocaleTimeString()}</Text>,
          }))}
          mode="start"
        />
      </div>
    </Card>
  );
}

/** One item's name as the operator knows it. */
function itemLabel(item: MigrationItemProgress, info: SourceInfo | null): string {
  switch (item.kind) {
    case 'database':
      return 'Panel database';
    case 'data-dir':
      return 'Data directory';
    case 'volume':
      return `Volume ${item.id.replace(/^volume-/, '')}`;
    case 'host-path':
      return 'Host path';
    case 'image': {
      const image = info?.images.find((i) => i.id === item.id)?.image;
      return `Image ${image?.tags[0] ?? item.id.replace(/^image-/, '')}`;
    }
  }
}

function errorText(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/**
 * The new host's half of a server Migration (ADR-0018), reached from the
 * setup screen: Connect, Preview, Progress, Done. It polls the pull's
 * progress, and keeps polling through the restart that swaps in the copied
 * database. After that the old admin account exists here, so a 401 sends the
 * visitor to sign in and back.
 */
export function MigrationPage() {
  const { message, modal } = App.useApp();
  const [progress, setProgress] = useState<MigrationProgress>();
  const [pollError, setPollError] = useState<string>();
  const [signIn, setSignIn] = useState(false);
  const [pending, setPending] = useState<string>();
  const [actionError, setActionError] = useState<string>();
  const phaseRef = useRef<string>('');

  const poll = useCallback(async () => {
    try {
      const next = await api.migrationProgress();
      phaseRef.current = next.phase;
      setProgress(next);
      setPollError(undefined);
    } catch (reason: unknown) {
      if (reason instanceof ApiError && reason.status === 401) {
        setSignIn(true);
        return;
      }
      // The panel exits and restarts while finalizing; errors then are expected.
      if (phaseRef.current !== 'finalizing') {
        setPollError(errorText(reason, 'Could not read the migration progress'));
      }
    }
  }, []);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      await poll();
      if (!stopped) timer = setTimeout(() => void tick(), POLL_MS);
    };
    void tick();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [poll]);

  useEffect(() => {
    if (signIn) message.info('Sign in with the admin account of the old server');
  }, [message, signIn]);

  if (signIn) {
    return <Navigate replace state={{ from: '/migration' }} to={paths.signIn} />;
  }

  const run = async (name: string, action: () => Promise<MigrationProgress | void>) => {
    setPending(name);
    setActionError(undefined);
    try {
      const next = await action();
      if (next) {
        phaseRef.current = next.phase;
        setProgress(next);
      }
    } catch (reason: unknown) {
      setActionError(errorText(reason, 'The migration request failed'));
    } finally {
      setPending(undefined);
    }
  };

  const abort = () => {
    modal.confirm({
      content: 'The old server resumes and starts its Composes again. Everything this server copied is deleted.',
      okButtonProps: { danger: true },
      okText: 'Abort migration',
      onOk: () =>
        run('abort', async () => {
          await api.migrationAbort();
          return api.migrationProgress();
        }),
      title: 'Abort the migration?',
    });
  };

  const phase = progress?.phase ?? '';
  let body: React.ReactNode;
  if (!progress) {
    body = <Spin aria-label="Loading" />;
  } else if (phase === '') {
    body = (
      <Form
        layout="vertical"
        onFinish={(values: { sourceUrl: string; token: string }) =>
          void run('connect', () => api.migrationConnect(values.sourceUrl.trim(), values.token.trim()))
        }
        requiredMark={false}
      >
        <Paragraph type="secondary">
          On the old panel, open Docker → Server migration and create a migration token. This panel then copies
          everything from it: the database, the data directory, every named Volume, the host paths you pick and the
          Images its Composes need.
        </Paragraph>
        <Form.Item
          label="Old panel URL"
          name="sourceUrl"
          rules={[{ message: 'Enter the old panel’s https:// address', required: true }]}
        >
          <Input autoComplete="off" placeholder="https://panel.example.com" />
        </Form.Item>
        <Form.Item label="Migration token" name="token" rules={[{ message: 'Paste the migration token', required: true }]}>
          <Input.Password autoComplete="off" />
        </Form.Item>
        <Flex gap={8} justify="space-between" wrap>
          {/* The panel's root sends a visitor to setup or sign-in as needed. */}
          <Link to={paths.overview}>
            <Button>Back</Button>
          </Link>
          <Button htmlType="submit" loading={pending === 'connect'} type="primary">
            Connect
          </Button>
        </Flex>
      </Form>
    );
  } else if (phase === 'connected') {
    body = <Preview onAbort={abort} pending={pending} progress={progress} run={run} />;
  } else if (phase === 'locking') {
    body = (
      <Flex gap={16} vertical>
        <Text>
          Locking the old server{progress.lockStep ? ` — ${progress.lockStep}` : ''}. Its Composes stop once a running
          Deployment ends.
        </Text>
        <Steps
          aria-label="Old server steps"
          current={Math.max(0, LOCK_STEPS.findIndex((s) => s.step === progress.lockStep))}
          items={LOCK_STEPS.map((s) => ({ title: s.title }))}
          orientation="vertical"
          size="small"
          status={progress.error ? 'error' : 'process'}
        />
        {progress.info?.activeDeployment && <Alert showIcon title={`Waiting for ${progress.info.activeDeployment}`} type="info" />}
        {progress.error && <Alert showIcon title={progress.error} type="error" />}
        <Flex gap={8} justify="flex-end">
          <Button danger loading={pending === 'abort'} onClick={abort}>
            Abort
          </Button>
          {progress.error && (
            <Button loading={pending === 'retry'} onClick={() => void run('retry', api.migrationRetry)} type="primary">
              Retry
            </Button>
          )}
        </Flex>
      </Flex>
    );
  } else if (phase === 'pulling' || phase === 'paused') {
    body = (
      <Flex gap={16} vertical>
        {phase === 'paused' && (
          <Alert
            description="An item failed three times. Retry it, or abort to resume the old server."
            showIcon
            title="The migration is paused"
            type="warning"
          />
        )}
        <Table<MigrationItemProgress>
          columns={[
            { key: 'item', render: (_, item) => itemLabel(item, progress.info), title: 'Item' },
            {
              key: 'state',
              render: (_, item) => (
                <Tag color={item.state === 'done' ? 'success' : item.state === 'failed' ? 'error' : item.state === 'running' ? 'processing' : 'default'}>
                  {item.state}
                </Tag>
              ),
              title: 'State',
              width: 110,
            },
            {
              key: 'progress',
              render: (_, item) => {
                const percent = item.size > 0 ? Math.min(100, Math.floor((item.done / item.size) * 100)) : item.state === 'done' ? 100 : 0;
                return (
                  <Flex gap={4} vertical>
                    <Progress percent={percent} size="small" status={item.state === 'failed' ? 'exception' : undefined} />
                    <Text type="secondary">
                      {formatBytes(item.done)} of {formatBytes(item.size)}
                    </Text>
                    {item.error && <Text type="danger">{item.error}</Text>}
                  </Flex>
                );
              },
              title: 'Progress',
            },
          ]}
          dataSource={progress.items}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
        <Flex gap={8} justify="flex-end">
          <Button danger loading={pending === 'abort'} onClick={abort}>
            Abort
          </Button>
          {phase === 'paused' && (
            <Button loading={pending === 'retry'} onClick={() => void run('retry', api.migrationRetry)} type="primary">
              Retry
            </Button>
          )}
        </Flex>
      </Flex>
    );
  } else if (phase === 'finalizing') {
    body = (
      <Flex align="center" gap={12} vertical>
        <Spin />
        <Text>Restarting the panel to swap in the migrated database…</Text>
        <Steps
          aria-label="Finalize steps"
          current={Math.max(0, FINALIZE_STEPS.findIndex((s) => s.step === progress.finalizeStep))}
          items={FINALIZE_STEPS.map((s) => ({ title: s.title }))}
          orientation="vertical"
          size="small"
          status={progress.error ? 'error' : 'process'}
        />
        <Text type="secondary">
          If this page does not come back within a few minutes, the panel container did not restart by itself: run docker
          compose up -d in the panel&apos;s directory on this server.
        </Text>
        {progress.error && <Alert showIcon action={<Button onClick={() => void run('retry', api.migrationRetry)}>Retry</Button>} title={progress.error} type="error" />}
      </Flex>
    );
  } else {
    const summary = progress.summary;
    body = (
      <Result
        extra={
          <Link to={paths.overview}>
            <Button type="primary">Open the panel</Button>
          </Link>
        }
        status="success"
        subTitle="Sign in with the admin account of the old server. Point these hosts at this server, then turn the old one off."
        title="Migration complete"
      >
        <Flex gap={16} vertical>
          {summary && summary.dns.length > 0 && (
            <Flex gap={4} vertical>
              <Text strong>DNS to change</Text>
              {summary.dns.map((host) => (
                <Text code copyable key={host}>
                  {host}
                </Text>
              ))}
            </Flex>
          )}
          {summary && summary.deployments.length > 0 && (
            <Flex gap={4} vertical>
              <Text strong>Deployments started</Text>
              {summary.deployments.map((d) =>
                d.error ? (
                  <Text key={d.composeId} type="danger">
                    Compose {d.composeId}: {d.error}
                  </Text>
                ) : (
                  <Link key={d.composeId} to={paths.deployments}>
                    Compose {d.composeId} · Deployment #{d.number}
                  </Link>
                ),
              )}
            </Flex>
          )}
          {[...(summary?.warnings ?? [])].map((w) => (
            <Alert key={w} showIcon title={w} type="warning" />
          ))}
        </Flex>
      </Result>
    );
  }

  return (
    <Layout style={{ alignItems: 'center', minHeight: '100vh', padding: 24 }}>
      <Card style={{ maxWidth: 960, width: '100%' }}>
        <Title level={3} style={{ marginTop: 0 }}>
          Migrate from another panel
        </Title>
        {pollError && phase !== 'finalizing' && <Alert showIcon style={{ marginBottom: 16 }} title={pollError} type="error" />}
        {actionError && <Alert showIcon style={{ marginBottom: 16 }} title={actionError} type="error" />}
        {progress && phase !== '' && <Journey progress={progress} />}
        {body}
        {progress && phase !== '' && progress.log.length > 0 && <ActivityLog log={progress.log} />}
      </Card>
    </Layout>
  );
}

function Preview({
  onAbort,
  pending,
  progress,
  run,
}: {
  onAbort: () => void;
  pending?: string;
  progress: MigrationProgress;
  run: (name: string, action: () => Promise<MigrationProgress | void>) => Promise<void>;
}) {
  const info = progress.info;
  const blocked = progress.checks.some((c) => c.blocking);
  const volumeBytes = info?.volumes.reduce((n, v) => n + v.size, 0) ?? 0;
  const imageBytes = info?.images.reduce((n, v) => n + v.size, 0) ?? 0;
  const copied = info?.images.filter((i) => (i.image?.repoDigests.length ?? 0) === 0).length ?? 0;

  return (
    <Flex gap={16} vertical>
      <Text type="secondary">Connected to {progress.sourceUrl}. Nothing there is locked until you start.</Text>
      {!info && (
        // The token was claimed but preflight failed: retry on the same session.
        <Alert
          action={
            <Button
              loading={pending === 'connect'}
              onClick={() => void run('connect', () => api.migrationConnect(progress.sourceUrl, ''))}
            >
              Retry
            </Button>
          }
          showIcon
          title={progress.error || 'Could not read the old panel'}
          type="error"
        />
      )}
      {progress.checks.map((c) => (
        <Alert key={c.message} showIcon title={c.message} type={c.blocking ? 'error' : 'warning'} />
      ))}
      {info && (
        <Descriptions
          column={{ md: 2, xs: 1 }}
          items={[
            { key: 'db', label: 'Panel database', children: formatBytes(info.databaseSize) },
            { key: 'dir', label: 'Data directory', children: formatBytes(info.dataDirSize) },
            { key: 'vol', label: 'Volumes', children: `${info.volumes.length} · ${formatBytes(volumeBytes)}` },
            {
              key: 'img',
              label: 'Images',
              children: `${info.images.length} · ${formatBytes(imageBytes)} · ${info.images.length - copied} pulled, ${copied} copied`,
            },
          ]}
          size="small"
        />
      )}
      {info && info.volumes.length > 0 && (
        <section aria-label="Volumes">
          <Table<MigrationItem>
            columns={[
              { key: 'volume', render: (_, item) => item.volume?.name, title: 'Volume' },
              { key: 'driver', render: (_, item) => item.volume?.driver || 'local', title: 'Driver', width: 120 },
              {
                key: 'options',
                render: (_, item) => {
                  const options = Object.entries(item.volume?.options ?? {});
                  return options.length > 0 ? (
                    <Text type="danger">{options.map(([k, v]) => `${k}=${v}`).join(', ')}</Text>
                  ) : (
                    <Text type="secondary">none</Text>
                  );
                },
                title: 'Options',
              },
              { key: 'size', render: (_, item) => formatBytes(item.size), title: 'Size', width: 120 },
            ]}
            dataSource={info.volumes}
            pagination={false}
            rowKey="id"
            scroll={{ x: 'max-content' }}
            size="small"
          />
        </section>
      )}
      {info && info.images.length > 0 && (
        <Table
          columns={[
            { key: 'image', render: (_, item) => item.image?.tags[0] ?? item.image?.id, title: 'Image' },
            { key: 'size', render: (_, item) => formatBytes(item.size), title: 'Size', width: 120 },
            {
              key: 'how',
              render: (_, item) => ((item.image?.repoDigests.length ?? 0) > 0 ? <Tag>pull</Tag> : <Tag color="blue">copy</Tag>),
              title: 'How',
              width: 90,
            },
          ]}
          dataSource={info.images}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
          size="small"
        />
      )}
      {info && info.hostPaths.length > 0 && (
        <Flex gap={8} vertical>
          <Text strong>Host paths outside the data directory</Text>
          <Text type="secondary">System paths are unticked: the new server keeps its own.</Text>
          <Table<HostPathCandidate>
            columns={[
              { dataIndex: 'path', key: 'path', title: 'Path' },
              { key: 'composes', render: (_, c) => c.composes.join(', '), title: 'Composes' },
              { key: 'size', render: (_, c) => (c.system ? '—' : formatBytes(c.size)), title: 'Size', width: 120 },
            ]}
            dataSource={info.hostPaths}
            pagination={false}
            rowKey="path"
            rowSelection={{
              onChange: (keys) => void run('recheck', () => api.migrationRecheck(keys.map(String))),
              selectedRowKeys: progress.selected,
            }}
            scroll={{ x: 'max-content' }}
            size="small"
          />
        </Flex>
      )}
      <Flex gap={8} justify="flex-end" wrap>
        <Button loading={pending === 'abort'} onClick={onAbort}>
          Cancel
        </Button>
        <Button
          disabled={blocked || !info || pending === 'recheck'}
          loading={pending === 'start'}
          onClick={() => void run('start', () => api.migrationStart(progress.selected))}
          type="primary"
        >
          Start migration
        </Button>
      </Flex>
    </Flex>
  );
}
