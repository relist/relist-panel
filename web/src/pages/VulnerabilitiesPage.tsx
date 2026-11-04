import { ReloadOutlined, SafetyOutlined, SettingOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Checkbox, Descriptions, Flex, Input, Select, Table, Tabs, Tag, Tooltip, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { VulnBadges } from '../components/VulnBadges';
import { VulnReportButton } from '../components/VulnReportButton';
import { api } from '../data/api';
import { usePanel } from '../data/store';
import type { VulnCve, VulnFinding, VulnImage, VulnSeverity, VulnStatus } from '../data/types';
import { flattenComposes } from './overview-view';
import {
  COPY,
  type ComposeNames,
  SEVERITIES,
  cveQueryFromSearch,
  cveQueryToSearch,
  dbFreshness,
  scanRowState,
  usedByLabel,
} from './vulnerabilities-view';
import { buildImageReport, reportFilename } from './vuln-report';
import { VulnerabilitySettingsDrawer } from './VulnerabilitySettingsDrawer';

const { Text } = Typography;

export const VULN_STATUS_KEY = '/api/vulnerabilities/status';
const VULN_IMAGES_KEY = '/api/vulnerabilities/images';

const SEVERITY_COLOR: Record<VulnSeverity, string | undefined> = {
  CRITICAL: 'error',
  HIGH: 'warning',
  MEDIUM: 'gold',
  LOW: undefined,
  UNKNOWN: undefined,
};

const SEVERITY_LABEL: Record<VulnSeverity, string> = {
  CRITICAL: 'Critical',
  HIGH: 'High',
  MEDIUM: 'Medium',
  LOW: 'Low',
  UNKNOWN: 'Unknown',
};

function SeverityTag({ severity }: { severity: VulnSeverity }) {
  return <Tag color={SEVERITY_COLOR[severity]}>{SEVERITY_LABEL[severity]}</Tag>;
}

function shortId(imageId: string): string {
  return imageId.replace(/^sha256:/, '').slice(0, 12);
}

function useComposeNames(): Map<number, ComposeNames> {
  const { projects } = usePanel();
  return useMemo(
    () =>
      new Map(
        flattenComposes(projects).map((c) => [
          c.compose.id,
          { projectName: c.projectName, environmentName: c.environmentName, composeName: c.compose.name },
        ]),
      ),
    [projects],
  );
}

/**
 * Vulnerabilities: what the Scanner found in every image in use (ADR-0021).
 * The Images tab answers "what should I rebuild?", the CVEs tab "how exposed
 * is the host?".
 */
export function VulnerabilitiesPage() {
  const { message } = App.useApp();
  const [params, setParams] = useSearchParams();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [scanning, setScanning] = useState(false);
  const status = useSWR(VULN_STATUS_KEY, () => api.vulnStatus(), {
    refreshInterval: (data) => (data?.running || (data?.queued ?? 0) > 0 ? 5000 : 60000),
  });
  const busy = Boolean(status.data?.running) || (status.data?.queued ?? 0) > 0;
  const images = useSWR(VULN_IMAGES_KEY, () => api.vulnImages(), { refreshInterval: busy ? 5000 : 0 });
  // The last scan in a run finishes between polls: reload once the queue
  // drains so its results show.
  const wasBusy = useRef(false);
  const reloadImages = images.mutate;
  useEffect(() => {
    if (wasBusy.current && !busy) {
      void reloadImages();
    }
    wasBusy.current = busy;
  }, [busy, reloadImages]);
  const enabled = status.data?.enabled ?? false;
  const tab = params.get('tab') === 'cves' ? 'cves' : 'images';

  const refresh = () => {
    void status.mutate();
    void images.mutate();
  };

  const scan = async (imageId?: string) => {
    setScanning(true);
    try {
      const { queued } = await api.vulnScan(imageId);
      message.success(queued === 1 ? 'Queued 1 image for scanning' : `Queued ${queued} images for scanning`);
      void status.mutate();
    } catch (reason) {
      message.error(reason instanceof Error ? reason.message : 'Could not queue the scan');
    } finally {
      setScanning(false);
    }
  };

  const setTab = (key: string) => {
    const next = new URLSearchParams(params);
    if (key === 'cves') next.set('tab', 'cves');
    else next.delete('tab');
    setParams(next);
  };

  return (
    <>
      <PageHeader
        description="Known CVEs in the images this host runs: the panel's Composes, the panel itself, Traefik and the Scanner."
        extra={
          <Flex gap={8} wrap>
            <Button aria-label="Refresh" icon={<ReloadOutlined />} loading={status.isLoading} onClick={refresh} />
            <Button icon={<SettingOutlined />} onClick={() => setSettingsOpen(true)}>
              Settings
            </Button>
            <Tooltip title={enabled ? undefined : COPY.off}>
              <Button disabled={!enabled} icon={<SafetyOutlined />} loading={scanning} onClick={() => void scan()} type="primary">
                Scan now
              </Button>
            </Tooltip>
          </Flex>
        }
        title="Vulnerabilities"
      />

      {status.error && (
        <Alert action={<Button onClick={refresh}>Retry</Button>} showIcon style={{ marginBottom: 16 }} title="Could not load the scan status" type="error" />
      )}
      {status.data && <StatusBar onOpenSettings={() => setSettingsOpen(true)} status={status.data} />}

      <Card variant="outlined">
        <Tabs
          activeKey={tab}
          items={[
            {
              key: 'images',
              label: 'Images',
              children: <ImagesTab enabled={enabled} error={images.error} images={images.data} loading={images.isLoading} onRetry={() => void images.mutate()} onScan={(id) => void scan(id)} />,
            },
            { key: 'cves', label: 'CVEs', children: <CvesTab images={images.data ?? []} params={params} setParams={setParams} /> },
          ]}
          onChange={setTab}
        />
      </Card>

      <VulnerabilitySettingsDrawer
        onClose={() => setSettingsOpen(false)}
        onSaved={() => {
          setSettingsOpen(false);
          refresh();
        }}
        open={settingsOpen}
        status={status.data}
      />
    </>
  );
}

function StatusBar({ onOpenSettings, status }: { onOpenSettings: () => void; status: VulnStatus }) {
  if (!status.enabled) {
    return (
      <Alert
        action={<Button onClick={onOpenSettings}>Open settings</Button>}
        description="Results from earlier scans stay visible. Turn scanning on to scan the images in use and keep them up to date."
        showIcon
        style={{ marginBottom: 16 }}
        title={COPY.off}
        type="info"
      />
    );
  }
  const freshness = dbFreshness(status);
  const queue = status.running
    ? `${status.running.kind === 'db_update' ? 'Updating the vulnerability DB' : `Scanning ${status.running.imageRef ?? shortId(status.running.imageId ?? '')}`}${status.queued > 0 ? `, ${status.queued} queued` : ''}`
    : status.queued > 0
      ? `${status.queued} queued, waiting for deployments to finish`
      : 'Idle';
  return (
    <>
      <Card style={{ marginBottom: 16 }} variant="outlined">
        <Descriptions
          column={{ xs: 1, md: 3 }}
          items={[
            { key: 'queue', label: 'Queue', children: queue },
            {
              key: 'db',
              label: 'Vulnerability DB',
              children:
                freshness === 'missing' ? (
                  <Text type="secondary">Not downloaded yet</Text>
                ) : (
                  <Flex gap={8} wrap>
                    <RelativeTime value={status.db.updatedAt ?? status.db.version} />
                    {freshness === 'stale' && <Tag color="warning">Out of date</Tag>}
                  </Flex>
                ),
            },
            {
              key: 'daily',
              label: 'Daily scan',
              children: status.nextDailyAt ? (
                <span>
                  Next <RelativeTime value={status.nextDailyAt} />
                </span>
              ) : (
                <Text type="secondary">Off</Text>
              ),
            },
          ]}
          size="small"
        />
      </Card>
      {status.db.error && (
        <Alert
          description={
            freshness === 'missing'
              ? 'Scans fail until a DB is downloaded. The next scan retries the download.'
              : 'Scans carry on with the DB already downloaded. Retry from the settings with Update vulnerability DB now.'
          }
          showIcon
          style={{ marginBottom: 16 }}
          title={`The last vulnerability DB update failed: ${status.db.error}`}
          type="warning"
        />
      )}
    </>
  );
}

function ImagesTab({
  enabled,
  error,
  images,
  loading,
  onRetry,
  onScan,
}: {
  enabled: boolean;
  error: unknown;
  images?: VulnImage[];
  loading: boolean;
  onRetry: () => void;
  onScan: (imageId: string) => void;
}) {
  const names = useComposeNames();
  const columns: TableColumnsType<VulnImage> = [
    {
      key: 'image',
      render: (_, image) => (
        <Flex vertical>
          <Text strong style={{ wordBreak: 'break-all' }}>{image.refs.length > 0 ? image.refs.join(', ') : shortId(image.imageId)}</Text>
          <Text type="secondary">{image.os || shortId(image.imageId)}</Text>
        </Flex>
      ),
      title: 'Image',
    },
    {
      key: 'users',
      render: (_, image) => (
        <Flex gap={4} vertical>
          {image.users.map((user, i) =>
            user.kind === 'service' && user.projectId && user.environmentId && user.composeId ? (
              <Link key={i} to={paths.compose(user.projectId, user.environmentId, user.composeId)}>
                {usedByLabel(user, names)}
              </Link>
            ) : (
              <span key={i}>{usedByLabel(user, names)}</span>
            ),
          )}
        </Flex>
      ),
      title: 'Used by',
    },
    {
      key: 'counts',
      render: (_, image) => {
        const state = scanRowState(image);
        return (
          <Flex gap={4} vertical>
            {image.counts && <VulnBadges badge={{ critical: image.counts.critical, high: image.counts.high, fixable: image.counts.fixable }} />}
            {state === 'never' && <Text type="secondary">{COPY.never}</Text>}
            {(state === 'failed' || state === 'failedWithResults') && <Text type="danger">{COPY.failed(image.error)}</Text>}
          </Flex>
        );
      },
      title: 'Vulnerabilities',
    },
    {
      key: 'scannedAt',
      render: (_, image) => (image.scannedAt ? <RelativeTime value={image.scannedAt} /> : <Text type="secondary">—</Text>),
      title: 'Scanned',
    },
    {
      key: 'action',
      render: (_, image) => (
        <Flex gap={8}>
          <Button disabled={!enabled} onClick={() => onScan(image.imageId)} size="small">
            Scan
          </Button>
          <VulnReportButton
            disabled={image.counts === null}
            load={async () => ({
              markdown: buildImageReport(image, await api.vulnFindings(image.imageId)),
              filename: reportFilename(image.refs[0] ?? shortId(image.imageId)),
            })}
            size="small"
          />
        </Flex>
      ),
      title: '',
    },
  ];
  if (error) {
    return <Alert action={<Button onClick={onRetry}>Retry</Button>} showIcon title="Could not list the images in use" type="error" />;
  }
  return (
    <Table<VulnImage>
      columns={columns}
      dataSource={images ?? []}
      expandable={{ expandedRowRender: (image) => <FindingsTable imageId={image.imageId} />, rowExpandable: (image) => image.counts !== null }}
      loading={loading}
      locale={{ emptyText: loading ? ' ' : <EmptyState description="No running container uses an image the panel scans." title="No images in use" /> }}
      pagination={false}
      rowKey="imageId"
      scroll={{ x: 'max-content' }}
    />
  );
}

function FindingsTable({ imageId }: { imageId: string }) {
  const { data, error, isLoading } = useSWR(['vuln-findings', imageId], () => api.vulnFindings(imageId));
  if (error) {
    return <Alert showIcon title="Could not load the findings" type="error" />;
  }
  return (
    <Table<VulnFinding>
      columns={[
        {
          key: 'id',
          render: (_, f) => (
            <Flex gap={4} wrap>
              {f.url ? (
                <a href={f.url} rel="noreferrer" target="_blank">
                  {f.vulnId}
                </a>
              ) : (
                f.vulnId
              )}
              {f.new && <Tag color="processing">New</Tag>}
            </Flex>
          ),
          title: 'ID',
        },
        { key: 'severity', render: (_, f) => <SeverityTag severity={f.severity} />, title: 'Severity' },
        {
          key: 'package',
          render: (_, f) => (
            <Flex vertical>
              <span>{f.pkgName}</span>
              {f.pkgPath && <Text type="secondary" style={{ wordBreak: 'break-all' }}>{f.pkgPath}</Text>}
            </Flex>
          ),
          title: 'Package',
        },
        {
          key: 'version',
          render: (_, f) => (f.fixedVersion ? `${f.installedVersion} → ${f.fixedVersion}` : <span>{f.installedVersion} <Text type="secondary">(no fix yet)</Text></span>),
          title: 'Installed → fixed',
        },
        { dataIndex: 'title', key: 'title', title: 'Title' },
      ]}
      dataSource={data ?? []}
      loading={isLoading}
      pagination={{ pageSize: 20, hideOnSinglePage: true }}
      rowKey={(f) => `${f.vulnId}|${f.pkgName}|${f.pkgPath}|${f.installedVersion}`}
      size="small"
    />
  );
}

function CvesTab({
  images,
  params,
  setParams,
}: {
  images: VulnImage[];
  params: URLSearchParams;
  setParams: (next: URLSearchParams) => void;
}) {
  const { projects } = usePanel();
  const query = cveQueryFromSearch(params);
  const { data, error, isLoading, mutate } = useSWR(['vuln-cves', JSON.stringify(query)], () => api.vulnCves(query));
  const [search, setSearch] = useState(query.q);
  const refsById = useMemo(() => new Map(images.map((i) => [i.imageId, i.refs[0] ?? shortId(i.imageId)])), [images]);

  const update = (change: Partial<typeof query>) => {
    const next = cveQueryToSearch({ ...query, page: 1, ...change });
    next.set('tab', 'cves');
    setParams(next);
  };

  const composeOptions = flattenComposes(projects).map((c) => ({
    label: `${c.projectName} / ${c.environmentName} / ${c.compose.name}`,
    value: c.compose.id,
  }));
  const columns: TableColumnsType<VulnCve> = [
    { dataIndex: 'vulnId', key: 'vulnId', render: (id: string, c) => <Flex gap={4} wrap><span>{id}</span>{c.new && <Tag color="processing">New</Tag>}</Flex>, title: 'CVE' },
    { key: 'severity', render: (_, c) => <SeverityTag severity={c.severity} />, title: 'Severity' },
    { dataIndex: 'title', key: 'title', title: 'Title' },
    { key: 'fix', render: (_, c) => (c.fixable ? <Tag color="success">Fix available</Tag> : <Text type="secondary">No fix yet</Text>), title: 'Fix' },
    { key: 'firstSeenAt', render: (_, c) => <RelativeTime value={c.firstSeenAt} />, title: 'First seen' },
    {
      key: 'images',
      render: (_, c) => <Text style={{ wordBreak: 'break-all' }}>{c.imageIds.map((id) => refsById.get(id) ?? shortId(id)).join(', ')}</Text>,
      title: 'Images',
    },
  ];

  return (
    <Flex gap={16} vertical>
      <Flex gap={8} wrap>
        <Select<VulnSeverity[]>
          aria-label="Severity"
          mode="multiple"
          onChange={(severity) => update({ severity })}
          options={SEVERITIES.map((s) => ({ label: SEVERITY_LABEL[s], value: s }))}
          placeholder="Any severity"
          style={{ minWidth: 200 }}
          value={query.severity}
        />
        <Select<number>
          allowClear
          aria-label="Project"
          onChange={(projectId) => update({ projectId, composeId: undefined })}
          options={projects.map((p) => ({ label: p.name, value: p.id }))}
          placeholder="Any Project"
          style={{ minWidth: 160 }}
          value={query.projectId}
        />
        <Select<number>
          allowClear
          aria-label="Compose"
          onChange={(composeId) => update({ composeId, projectId: undefined })}
          options={composeOptions}
          placeholder="Any Compose"
          style={{ minWidth: 220 }}
          value={query.composeId}
        />
        <Checkbox checked={query.fixable} onChange={(e) => update({ fixable: e.target.checked })}>
          Fixable only
        </Checkbox>
        <Checkbox checked={query.new} onChange={(e) => update({ new: e.target.checked })}>
          New in 7 days
        </Checkbox>
        <Input.Search
          allowClear
          aria-label="Search CVEs"
          onChange={(e) => setSearch(e.target.value)}
          onSearch={(q) => update({ q })}
          placeholder="CVE, package or title"
          style={{ width: 240 }}
          value={search}
        />
      </Flex>
      {error && <Alert action={<Button onClick={() => void mutate()}>Retry</Button>} showIcon title="Could not load CVEs" type="error" />}
      <Table<VulnCve>
        columns={columns}
        dataSource={data?.items ?? []}
        loading={isLoading}
        locale={{ emptyText: isLoading ? ' ' : 'No CVEs match these filters' }}
        onChange={(pagination) => update({ page: pagination.current ?? 1, pageSize: pagination.pageSize ?? query.pageSize })}
        pagination={{ current: query.page, pageSize: query.pageSize, total: data?.total ?? 0, showSizeChanger: true }}
        rowKey="vulnId"
        scroll={{ x: 'max-content' }}
      />
    </Flex>
  );
}
