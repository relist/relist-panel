import { Alert, App, Button, Card, Descriptions, Flex, Form, Input, Layout, Progress, Result, Spin, Table, Tag, Typography } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, Navigate } from 'react-router';

import { paths } from '../app/paths';
import { formatBytes } from '../components/MetricChart';
import { ApiError, api } from '../data/api';
import type { HostPathCandidate, MigrationItemProgress, MigrationProgress, SourceInfo } from '../data/migration';

const { Paragraph, Text, Title } = Typography;

const POLL_MS = 2000;

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
          <Link to={paths.setup}>
            <Button>Back to setup</Button>
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
        {progress.info?.activeDeployment && <Alert showIcon title={`Waiting for ${progress.info.activeDeployment}`} type="info" />}
        {progress.error && <Alert showIcon title={progress.error} type="error" />}
        <Flex gap={8} justify="flex-end">
          <Button danger loading={pending === 'abort'} onClick={abort}>
            Abort
          </Button>
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
        {body}
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
          disabled={blocked || pending === 'recheck'}
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
