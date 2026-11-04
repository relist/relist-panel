import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Empty, Flex, InputNumber, Segmented, Spin, Switch, Table, Tabs, Typography, theme } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useRef, useState } from 'react';
import useSWR from 'swr';

import { formatBytes } from '../components/MetricChart';
import { TraefikApplyModal } from '../components/TraefikApplyModal';
import type { TraefikReview } from '../components/TraefikApplyModal';
import { TraefikConfigTab } from '../components/TraefikConfigTab';
import { PageHeader } from '../components/primitives';
import { ApiError, accessLogDownloadUrl, api } from '../data/api';
import type { AccessLogFile } from '../data/types';
import { canSaveRetention, filterLogLines } from './traefik-view';
import type { LogLevel } from './traefik-view';

const { Text } = Typography;

const DESCRIPTION =
  "Traefik's own log, where Let's Encrypt errors show up, and the Access log of every request, rotated daily and kept for the period set below.";
const NOT_FOUND = 'No Traefik container found in this install.';

const LEVELS: { label: string; value: LogLevel }[] = [
  { label: 'All', value: 'all' },
  { label: 'Error', value: 'error' },
  { label: 'Warn', value: 'warn' },
  { label: 'Info', value: 'info' },
];

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

type TabKey = 'logs' | 'access' | 'config';

/**
 * Traefik: its own log, the Access log files and how long they are kept
 * (ADR-0027), and its static config (ADR-0030). The panel rotates the Access
 * log; this page only reads it.
 */
export function TraefikPage() {
  const { token } = theme.useToken();
  const status = useSWR('/api/traefik/status', () => api.traefikStatus());
  const logs = useSWR('/api/traefik/logs', () => api.traefikLogs());
  const files = useSWR('/api/traefik/access-logs', () => api.listAccessLogs());
  const settings = useSWR('/api/traefik/access-log-settings', () => api.accessLogSettings());
  const config = useSWR('/api/traefik/config', () => api.traefikConfig());
  const [level, setLevel] = useState<LogLevel>('all');
  const [tab, setTab] = useState<TabKey>('logs');
  const [review, setReview] = useState<TraefikReview | null>(null);
  const [runId, setRunId] = useState<string | null>(null);
  // Runs this page has already shown, so a revalidation does not reopen one.
  const shown = useRef(new Set<string>());

  const follow = (id: string) => {
    shown.current.add(id);
    setReview(null);
    setRunId(id);
  };

  // An apply nobody has seen the end of (it ran while the page was away, or the
  // page reloaded mid-run) opens its modal.
  const lastApply = config.data?.lastApply;
  useEffect(() => {
    if (lastApply && !lastApply.acked && !shown.current.has(lastApply.id)) follow(lastApply.id);
  }, [lastApply]);

  const refresh = () => {
    void status.mutate();
    void logs.mutate();
    void files.mutate();
    void settings.mutate();
    void config.mutate();
  };

  const lines = logs.data ? filterLogLines(logs.data.logs, level) : [];
  const columns: TableColumnsType<AccessLogFile> = [
    { dataIndex: 'date', key: 'date', render: (date: string | null) => date ?? <Text type="secondary">Today</Text>, title: 'Date' },
    { dataIndex: 'name', key: 'name', render: (name: string) => <Text code>{name}</Text>, title: 'File' },
    { align: 'right', dataIndex: 'size', key: 'size', render: (size: number) => formatBytes(size), title: 'Size' },
    {
      align: 'right',
      key: 'actions',
      render: (_, file) => (
        <Button aria-label={`Download ${file.name}`} href={accessLogDownloadUrl(file.name)} icon={<DownloadOutlined />} type="text" />
      ),
      title: '',
    },
  ];

  return (
    <>
      <PageHeader
        description={DESCRIPTION}
        extra={
          <Button
            icon={<ReloadOutlined />}
            loading={status.isValidating || logs.isValidating || files.isValidating || settings.isValidating || config.isValidating}
            onClick={refresh}
          >
            Refresh
          </Button>
        }
        title="Traefik"
      />

      {status.error && (
        <Alert
          showIcon
          style={{ marginBottom: 16 }}
          title={errorMessage(status.error, 'Could not check how Traefik was started')}
          type="error"
        />
      )}

      <Tabs
        activeKey={tab}
        items={[
          {
            children: (
              <Card
                extra={<Segmented<LogLevel> onChange={setLevel} options={LEVELS} size="small" value={level} />}
                title="Traefik log"
                variant="outlined"
              >
                {logs.error ? (
                  <Alert showIcon title={errorMessage(logs.error, "Could not read Traefik's log")} type="error" />
                ) : logs.data && !logs.data.found ? (
                  <Alert showIcon title={NOT_FOUND} type="info" />
                ) : lines.length === 0 ? (
                  <Empty description={<Text type="secondary">No log lines at this level.</Text>} image={Empty.PRESENTED_IMAGE_SIMPLE} />
                ) : (
                  <pre
                    className="rp-logs"
                    style={{
                      background: token.colorFillQuaternary,
                      border: `1px solid ${token.colorBorderSecondary}`,
                      borderRadius: token.borderRadius,
                      margin: 0,
                      maxHeight: 480,
                      overflow: 'auto',
                    }}
                  >
                    {lines.map((line, index) => (
                      <div key={`${index}-${line.slice(0, 32)}`}>{line}</div>
                    ))}
                  </pre>
                )}
              </Card>
            ),
            key: 'logs',
            label: 'Logs',
          },
          {
            children: (
              <Flex gap={16} vertical>
                {status.data?.found && !status.data.accessLogEnabled && (
                  <Alert
                    action={
                      <Button onClick={() => setTab('config')} size="small" type="link">
                        Turn it on in Settings
                      </Button>
                    }
                    showIcon
                    title="Access log is off."
                    type="info"
                  />
                )}
                <Card
                  extra={files.data && <Text type="secondary">{formatBytes(files.data.totalSize)} on disk</Text>}
                  title="Access logs"
                  variant="outlined"
                >
                  {files.error && (
                    <Alert showIcon style={{ marginBottom: 16 }} title={errorMessage(files.error, 'Could not list Access log files')} type="error" />
                  )}
                  <Table<AccessLogFile>
                    columns={columns}
                    dataSource={files.data?.files ?? []}
                    loading={files.isLoading}
                    locale={{ emptyText: files.isLoading ? ' ' : 'No Access log files yet.' }}
                    pagination={false}
                    rowKey="name"
                    scroll={{ x: 'max-content' }}
                    size="small"
                  />
                  {settings.data?.lastRotatedAt && (
                    <Text style={{ display: 'block', marginTop: 12 }} type="secondary">
                      Last rotation: {new Date(settings.data.lastRotatedAt).toLocaleString()}
                    </Text>
                  )}
                  {settings.data?.lastError && (
                    <Alert showIcon style={{ marginTop: 12 }} title={`Last rotation failed: ${settings.data.lastError}`} type="error" />
                  )}
                </Card>

                {settings.data && (
                  <RetentionCard
                    keepForever={settings.data.keepForever}
                    onSaved={() => void settings.mutate()}
                    retentionDays={settings.data.retentionDays}
                  />
                )}
              </Flex>
            ),
            key: 'access',
            label: 'Access logs',
          },
          {
            children: config.error ? (
              <Alert showIcon title={errorMessage(config.error, "Could not read Traefik's config")} type="error" />
            ) : config.data ? (
              <TraefikConfigTab
                config={config.data}
                onReviewApply={(draft, effective) =>
                  config.data &&
                  setReview({ current: config.data.effective, draft, effective, revision: config.data.revision })
                }
                onStarted={follow}
              />
            ) : (
              <Spin />
            ),
            key: 'config',
            label: 'Configuration',
          },
        ]}
        onChange={(key) => setTab(key as TabKey)}
      />

      {(review !== null || runId !== null) && (
        <TraefikApplyModal
          onClose={() => {
            setReview(null);
            setRunId(null);
          }}
          onFinished={() => {
            void config.mutate();
            void status.mutate();
          }}
          review={review}
          runId={runId}
        />
      )}
    </>
  );
}

function RetentionCard({
  keepForever: savedForever,
  onSaved,
  retentionDays: savedDays,
}: {
  keepForever: boolean;
  onSaved: () => void;
  retentionDays: number;
}) {
  const { message } = App.useApp();
  const [days, setDays] = useState<number | null>(savedDays);
  const [forever, setForever] = useState(savedForever);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setDays(savedDays);
    setForever(savedForever);
  }, [savedDays, savedForever]);

  const save = async () => {
    if (days === null) return;
    setSaving(true);
    try {
      await api.saveAccessLogSettings({ keepForever: forever, retentionDays: days });
      onSaved();
      void message.success('Retention saved');
    } catch (reason: unknown) {
      void message.error(reason instanceof ApiError ? reason.message : 'Could not save retention');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card title="Retention" variant="outlined">
      <Flex align="center" gap={16} wrap>
        <InputNumber
          aria-label="Days to keep"
          disabled={forever}
          min={1}
          onChange={(value) => setDays(typeof value === 'number' ? value : null)}
          precision={0}
          suffix="days"
          value={days}
        />
        <Flex align="center" gap={8}>
          <Switch aria-label="Keep forever" checked={forever} onChange={setForever} />
          <Text>Keep forever</Text>
        </Flex>
        <Button
          disabled={!canSaveRetention(days, forever, { keepForever: savedForever, retentionDays: savedDays })}
          loading={saving}
          onClick={() => void save()}
          type="primary"
        >
          Save
        </Button>
      </Flex>
    </Card>
  );
}
