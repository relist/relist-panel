import {
  CloudDownloadOutlined,
  CopyOutlined,
  DeleteOutlined,
  FileTextOutlined,
  FullscreenOutlined,
  MoreOutlined,
  ReloadOutlined,
  RedoOutlined,
  RocketOutlined,
  SaveOutlined,
  StopOutlined,
  UndoOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Drawer,
  Dropdown,
  Empty,
  Flex,
  Result,
  Row,
  Select,
  Skeleton,
  Space,
  Spin,
  Switch,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import type { MenuProps } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router';

import { paths } from '../app/paths';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { ComposeVolumesTab } from '../components/ComposeVolumesTab';
import { DangerModal } from '../components/DangerModal';
import { FullscreenEditor } from '../components/FullscreenEditor';
import {
  CodeValue,
  ComposeStatusTag,
  EmptyState,
  HealthBadge,
  PageHeader,
  RelativeTime,
  ServiceStateTag,
} from '../components/primitives';
import { ServiceMonitor } from '../components/ServiceMonitor';
import { YamlEditor } from '../components/YamlEditor';
import { volumeArchiveUrl } from '../data/api';
import { usePanel } from '../data/store';
import type { ComposeVolumes } from '../data/types';
import { composeState, findComposeLocation } from '../data/types';
import { activeComposeTab, composeDeployIdentity, composeLinkIsStale, deploySteps, filterLogLines, hasUnsavedChanges } from './compose-view';

const { Text, Paragraph, Title } = Typography;

/**
 * Compose detail (GUIDELINES §4.6–4.7), the port of the prototype's Compose
 * page onto the real store: lifecycle actions are graded by weight (one
 * primary, one "More"), the `Editor | Monitor | Volumes` tab lives in the path
 * so each one is linkable, and the right rail answers "what is running right
 * now, and where does this thing live on the host?" without leaving the screen.
 * The Monitor tab answers how a Service has been behaving, fed by the same
 * Services endpoint as the rail's rows; the Volumes tab lists the Volumes this
 * Compose resolves to and offers each one's tar.gz backup — read-only, and
 * hidden outright when the panel cannot run the feature at all.
 *
 * The page scrolls as one document. The editor and the rail stretch to the same
 * height, so the editor is as tall as the status rail next to it (never shorter
 * than `LAYOUT.editorMinHeight`); the rail simply continues below the fold.
 */
export function ComposePage() {
  const { composeId: composeParam, environmentId: environmentParam, projectId: projectParam, tab } = useParams();
  const composeId = Number(composeParam);
  const projectId = Number(projectParam);
  const environmentId = Number(environmentParam);
  const navigate = useNavigate();
  const {
    checkedAt,
    deleteCompose,
    deployCompose,
    isPending,
    loadLogs,
    loadVariables,
    loadServices,
    loadVolumes,
    projects,
    refreshStatus,
    restartCompose,
    saveCompose,
    services: servicesByCompose,
    stopCompose,
  } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();

  const location = findComposeLocation(projects, composeId);
  const compose = location?.compose;
  const environment = location?.environment;
  const project = location?.project;

  const [draft, setDraft] = useState(compose?.yamlContent ?? '');
  const [fullscreenOpen, setFullscreenOpen] = useState(false);
  const [logsOpen, setLogsOpen] = useState(false);
  const [logsText, setLogsText] = useState<string>();
  const [logServices, setLogServices] = useState<string[]>([]);
  const [logLines, setLogLines] = useState(200);
  const [deleteOpen, setDeleteOpen] = useState(false);
  // Action failures stay on this screen as an Alert (GUIDELINES §8) — the
  // control that retries them is right there.
  const [actionError, setActionError] = useState<string>();
  const fail = (reason: unknown) =>
    setActionError(reason instanceof Error ? reason.message : 'Something went wrong');

  // The Volumes tab's resolution is fetched once here: the tab is hidden when
  // the feature cannot run at all (`available: false`), and that is a
  // page-level decision, so the page owns the fetch and hands the result down.
  // State is keyed to its Compose so a slow answer never shows on another one,
  // and the request counter drops the answer outright.
  const [volumes, setVolumes] = useState<{ composeId: number; payload?: ComposeVolumes; error?: string }>();
  const volumesRequest = useRef(0);
  const volumesForCompose = volumes?.composeId === composeId ? volumes : undefined;
  const volumesPayload = volumesForCompose?.payload;
  const volumesError = volumesForCompose?.error;

  useEffect(() => {
    setDraft(compose?.yamlContent ?? '');
  }, [compose?.id, compose?.yamlContent]);

  useEffect(() => {
    setLogsText(undefined);
    setLogServices([]);
  }, [composeId]);

  // The full screen editor must never outlive the Compose it was opened for.
  useEffect(() => {
    setFullscreenOpen(false);
  }, [composeId]);

  // The Environment card and the full screen editor's rail read the
  // Environment's variables; the tree ships with them unloaded.
  const variablesEnvironmentId = environment?.id;
  useEffect(() => {
    if (variablesEnvironmentId !== undefined) {
      loadVariables(variablesEnvironmentId).catch(fail);
    }
  }, [loadVariables, variablesEnvironmentId]);

  // The URL is the source of truth for navigation, so a stale path (renamed
  // parent, hand-edited URL) is redirected to the canonical one — tab included,
  // or the redirect would drop the operator back on the Editor.
  const activeTab = activeComposeTab(tab);
  // `available: false` means the feature cannot run at all — a bare binary, no
  // Docker socket — and the tab is hidden rather than shown dead. A URL that
  // still names it redirects to the Editor along with every other stale link.
  const volumesUnavailable = volumesPayload !== undefined && !volumesPayload.available;
  const canonicalTab =
    activeTab === 'editor' || (activeTab === 'volumes' && volumesUnavailable) ? undefined : activeTab;
  const canonicalPath =
    compose && environment && project
      ? paths.compose(project.id, environment.id, compose.id, canonicalTab)
      : undefined;
  const pathIsStale =
    composeLinkIsStale(location, projectId, environmentId) || (activeTab === 'volumes' && volumesUnavailable);
  useEffect(() => {
    if (pathIsStale && canonicalPath) {
      navigate(canonicalPath);
    }
  }, [canonicalPath, navigate, pathIsStale]);

  const dirty = compose ? hasUnsavedChanges(draft, compose.yamlContent) : false;

  // Per-Service rows come from the Services endpoint (the container
  // collector's cache) — the same rows the Monitor tab's picker reads.
  const services = servicesByCompose[composeId] ?? [];

  const fetchServices = useCallback(() => {
    loadServices(composeId).catch((reason: unknown) => {
      setActionError(reason instanceof Error ? reason.message : 'Something went wrong');
    });
  }, [composeId, loadServices]);

  // One resolution per page load (the spec: ~30 ms through the Compose CLI),
  // refetched on Retry and after a deploy writes the env file. A retry clears
  // the payload, so the tab shows its skeleton rather than a stale list.
  const fetchVolumes = useCallback(() => {
    const request = ++volumesRequest.current;
    setVolumes({ composeId });
    return loadVolumes(composeId)
      .then((payload) => {
        if (volumesRequest.current === request) {
          setVolumes({ composeId, payload });
        }
      })
      .catch((reason: unknown) => {
        if (volumesRequest.current === request) {
          setVolumes({
            composeId,
            error: reason instanceof Error ? reason.message : 'Could not resolve this Compose’s Volumes',
          });
        }
      });
  }, [composeId, loadVolumes]);

  // The rows follow the containers: load once the deep link has resolved its
  // Compose (the tree ships later than this page on a cold URL — the same gate
  // the variables load uses), and again whenever the host may have changed.
  const knownComposeId = compose?.id;
  useEffect(() => {
    if (knownComposeId !== undefined) {
      fetchServices();
    }
  }, [fetchServices, knownComposeId]);

  // The Volumes tab cannot ask for its own data — whether the tab exists at
  // all depends on the answer — so the page loads it once per Compose, behind
  // the same deep-link gate as the Services rows.
  useEffect(() => {
    if (knownComposeId !== undefined) {
      void fetchVolumes();
    }
  }, [fetchVolumes, knownComposeId]);

  const logsLoading = isPending(`logs:${composeId}`);
  const logLineCount = logsText ? logsText.split('\n').length : 0;
  const visibleLogLines = filterLogLines(logsText, logServices, logLines);

  if (!compose || !environment || !project) {
    // A deep link can land before the Project tree has loaded; a loading card
    // covers that window, the design's not-found covers a stale link.
    if (isPending('projects') && projects.length === 0) {
      return (
        <Card variant="outlined">
          <Flex justify="center" style={{ padding: '48px 0' }}>
            <Spin size="large" />
          </Flex>
        </Card>
      );
    }
    return (
      <Result
        extra={
          <Button onClick={() => navigate(paths.projects)} type="primary">
            Back to Projects
          </Button>
        }
        status="404"
        subTitle="It may have been deleted, or the link is stale."
        title="Compose not found"
      />
    );
  }

  const state = composeState(compose.status, compose.lastDeployedAt);
  const deployIdentity = composeDeployIdentity(compose);
  const checked = checkedAt[compose.id];

  const save = async () => {
    try {
      await saveCompose(compose.id, draft);
      message.success('Saved docker-compose.yml');
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const deploy = async () => {
    try {
      for (const step of deploySteps(dirty)) {
        if (step === 'save') {
          await saveCompose(compose.id, draft);
        } else {
          await deployCompose(compose.id);
        }
      }
      message.success(`Deployed ${compose.name}`);
      // A deploy mints new containers; the rail's rows follow, and the env
      // file it wrote is what the Volumes resolution needs.
      fetchServices();
      void fetchVolumes();
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const lifecycle = async (action: 'restart' | 'stop') => {
    try {
      if (action === 'restart') {
        await restartCompose(compose.id);
      } else {
        await stopCompose(compose.id);
      }
      message.success(`${action === 'restart' ? 'Restarted' : 'Stopped'} ${compose.name}`);
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const openLogs = async () => {
    setLogsOpen(true);
    try {
      setLogsText(await loadLogs(compose.id));
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const removeCompose = async () => {
    try {
      await deleteCompose(compose.id);
      setDeleteOpen(false);
      message.success(`Deleted ${compose.name}`);
      navigate(paths.environment(project.id, environment.id));
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const moreMenu: MenuProps['items'] = [
    {
      disabled: compose.status.total === 0,
      icon: <RedoOutlined />,
      key: 'restart',
      label: 'Restart containers',
      onClick: () => void lifecycle('restart'),
    },
    {
      disabled: compose.status.total === 0,
      icon: <StopOutlined />,
      key: 'stop',
      label: 'Stop containers',
      onClick: () => void lifecycle('stop'),
    },
    { type: 'divider' },
    { danger: true, icon: <DeleteOutlined />, key: 'delete', label: 'Delete Compose', onClick: () => setDeleteOpen(true) },
  ];

  return (
    <>
      <PageHeader
        description={
          <Flex align="center" gap={6} wrap>
            <span>
              {project.name} / {environment.name}
            </span>
            <span>·</span>
            <span>
              Last edited <RelativeTime value={compose.updatedAt} />
            </span>
            {compose.lastDeployedAt && (
              <>
                <span>·</span>
                <span>
                  Deployed <RelativeTime value={compose.lastDeployedAt} />
                </span>
              </>
            )}
          </Flex>
        }
        extra={
          <>
            <Button icon={<FileTextOutlined />} onClick={() => void openLogs()}>
              Logs
            </Button>
            <Dropdown menu={{ items: moreMenu }} placement="bottomRight" trigger={['click']}>
              <Button icon={<MoreOutlined />} />
            </Dropdown>
            <Button
              disabled={!dirty}
              icon={<SaveOutlined />}
              loading={isPending(`save-compose:${compose.id}`)}
              onClick={() => void save()}
            >
              Save
            </Button>
            <Button
              icon={<RocketOutlined />}
              loading={isPending(`deploy:${compose.id}`)}
              onClick={() => void deploy()}
              type="primary"
            >
              {dirty ? 'Save & Deploy' : 'Deploy'}
            </Button>
          </>
        }
        tags={
          <>
            <ComposeStatusTag lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
            {dirty && (
              <Tag color="processing" variant="filled">
                Unsaved changes
              </Tag>
            )}
          </>
        }
        title={compose.name}
      />

      {actionError && (
        <Alert
          afterClose={() => setActionError(undefined)}
          closable
          message={actionError}
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          type="error"
        />
      )}

      <Tabs
        activeKey={activeTab}
        items={[
          {
            children: (
              // No `align="top"` here: the columns stretch to the same height, which is
              // what lets the editor follow the rail instead of picking a height itself.
              <Row gutter={[LAYOUT.gutter, LAYOUT.gutter]}>
                <Col lg={15} xs={24}>
                  <Card
                    style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: LAYOUT.editorMinHeight }}
                    styles={{ body: { display: 'flex', flex: 1, flexDirection: 'column', minHeight: 0, padding: 0 } }}
                    title={
                      <Flex align="center" gap={8} justify="space-between" style={{ width: '100%' }}>
                        <Flex align="center" gap={8}>
                          <FileTextOutlined style={{ color: token.colorTextTertiary }} />
                          <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>docker-compose.yml</Text>
                        </Flex>
                        <Space>
                          {dirty && (
                            <Text type="warning" style={{ fontSize: 12 }}>
                              Modified
                            </Text>
                          )}
                          <Tooltip title="Discard local edits and reload the saved file">
                            <Button
                              disabled={!dirty}
                              icon={<UndoOutlined />}
                              onClick={() => setDraft(compose.yamlContent)}
                              size="small"
                            >
                              Discard
                            </Button>
                          </Tooltip>
                          <Tooltip title="Edit docker-compose.yml full screen, with the Environment variables beside it">
                            <Button
                              aria-label="Full screen editor"
                              icon={<FullscreenOutlined />}
                              onClick={() => setFullscreenOpen(true)}
                              size="small"
                              type="text"
                            />
                          </Tooltip>
                        </Space>
                      </Flex>
                    }
                    variant="outlined"
                  >
                    {dirty && (
                      <Alert
                        action={
                          <Space>
                            <Button onClick={() => setDraft(compose.yamlContent)} size="small">
                              Discard
                            </Button>
                            <Button
                              loading={isPending(`save-compose:${compose.id}`)}
                              onClick={() => void save()}
                              size="small"
                              type="primary"
                            >
                              Save
                            </Button>
                          </Space>
                        }
                        banner
                        message="You have unsaved changes. Deploying will save them first."
                        type="warning"
                      />
                    )}
                    <div style={{ flex: 1, minHeight: 0 }}>
                      <YamlEditor
                        onChange={setDraft}
                        path={`${project.id}-${environment.id}-${compose.id}.yml`}
                        value={draft}
                      />
                    </div>
                  </Card>
                </Col>

                <Col lg={9} xs={24}>
                  <Flex gap={LAYOUT.gutterSmall} vertical>
                    <Card
                      extra={
                        <Tooltip title="Re-read container status from the host">
                          <Button
                            icon={<ReloadOutlined />}
                            loading={isPending(`status:${compose.id}`) || isPending(`services:${compose.id}`)}
                            onClick={() => {
                              void refreshStatus(compose.id).catch(fail);
                              fetchServices();
                            }}
                            size="small"
                            type="text"
                          />
                        </Tooltip>
                      }
                      title="Status"
                      variant="outlined"
                    >
                      <Flex align="center" gap={8} justify="space-between" style={{ marginBottom: 12 }}>
                        <ComposeStatusTag lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          {checked ? (
                            <>
                              Checked <RelativeTime value={checked} />
                            </>
                          ) : (
                            'Not checked yet'
                          )}
                        </Text>
                      </Flex>

                      {services.length > 0 ? (
                        <Flex gap={10} vertical>
                          {services.map((service) => (
                            <div key={service.name}>
                              <Flex align="center" gap={6} justify="space-between">
                                <Flex align="center" gap={6}>
                                  <Text strong style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>
                                    {service.name}
                                  </Text>
                                  <HealthBadge health={service.health} />
                                </Flex>
                                <ServiceStateTag service={service} />
                              </Flex>
                              <Text ellipsis style={{ display: 'block', fontFamily: CODE_FONT_STACK, fontSize: 11 }} type="secondary">
                                {service.image}
                              </Text>
                              {service.ports.length > 0 && (
                                <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 11 }} type="secondary">
                                  {service.ports.join(', ')}
                                </Text>
                              )}
                            </div>
                          ))}
                        </Flex>
                      ) : compose.status.total === 0 ? (
                        <Empty
                          description={<Text type="secondary">No containers on the host for this Compose.</Text>}
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          styles={{ image: { height: 40 } }}
                        >
                          <Button
                            icon={<RocketOutlined />}
                            loading={isPending(`deploy:${compose.id}`)}
                            onClick={() => void deploy()}
                            size="small"
                            type="primary"
                          >
                            Deploy now
                          </Button>
                        </Empty>
                      ) : (
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          The collector has not read these containers yet — rows appear at its next sweep, every 30
                          seconds. The counts above are what it reads today.
                        </Text>
                      )}

                      {state === 'partial' && (
                        <Alert
                          description="Open the Logs drawer and filter to the service that exited."
                          message="Some containers are not running"
                          showIcon
                          style={{ marginTop: 12 }}
                          type="warning"
                        />
                      )}
                    </Card>

                    <Card title="Deploy identity" variant="outlined">
                      <Paragraph style={{ fontSize: 12 }} type="secondary">
                        Frozen at Compose creation (ADR-0011). Renaming the Project or Environment does not move what is
                        already deployed.
                      </Paragraph>
                      <Flex gap={10} vertical>
                        <div>
                          <Text strong style={{ fontSize: 12 }}>
                            Files on disk
                          </Text>
                          <CodeValue
                            copyable
                            value={deployIdentity.directory}
                          />
                        </div>
                        <div>
                          <Text strong style={{ fontSize: 12 }}>
                            docker compose project
                          </Text>
                          <CodeValue copyable value={deployIdentity.projectName} />
                        </div>
                      </Flex>
                    </Card>

                    <Card
                      extra={
                        <Button
                          onClick={() => navigate(paths.environment(project.id, environment.id, 'variables'))}
                          size="small"
                          type="link"
                        >
                          Manage
                        </Button>
                      }
                      title="Environment"
                      variant="outlined"
                    >
                      <Descriptions
                        column={1}
                        items={[
                          { children: environment.name, key: 'name', label: 'Environment' },
                          {
                            children: (
                              <Text>
                                {environment.variables.length} variable
                                {environment.variables.length === 1 ? '' : 's'} applied at deploy time
                              </Text>
                            ),
                            key: 'vars',
                            label: 'Variables',
                          },
                          {
                            children: (
                              <Text code style={{ fontSize: 12 }}>
                                {`\${${environment.variables[0]?.key ?? 'KEY'}}`}
                              </Text>
                            ),
                            key: 'usage',
                            label: 'Reference as',
                          },
                        ]}
                        size="small"
                      />
                    </Card>
                  </Flex>
                </Col>
              </Row>
            ),
            key: 'editor',
            label: 'Editor',
          },
          {
            children: (
              <ServiceMonitor
                compose={compose}
                onOpenEditor={() => navigate(paths.compose(project.id, environment.id, compose.id))}
              />
            ),
            key: 'monitor',
            label: 'Monitor',
          },
          // Hidden outright when the feature cannot run at all — a bare binary
          // or no Docker socket — per the spec's security decision 10.
          ...(volumesUnavailable
            ? []
            : [
                {
                  children: (
                    <ComposeVolumesTab
                      archiveHref={(volume, access) =>
                        volumeArchiveUrl(
                          { composeId: compose.id, environmentId: environment.id, projectId: project.id },
                          volume.source,
                          '.',
                          access,
                        )
                      }
                      deploying={isPending(`deploy:${compose.id}`)}
                      error={volumesError}
                      explorerHref={(volume) =>
                        paths.volumeExplorer(project.id, environment.id, compose.id, volume.source)
                      }
                      onDeploy={() => void deploy()}
                      onRetry={() => void fetchVolumes()}
                      payload={volumesPayload}
                    />
                  ),
                  key: 'volumes',
                  label: 'Volumes',
                },
              ]),
        ]}
        onChange={(key) =>
          navigate(paths.compose(project.id, environment.id, compose.id, key === 'editor' ? undefined : key))
        }
      />

      <FullscreenEditor
        compose={compose}
        dirty={dirty}
        draft={draft}
        environment={environment}
        onChange={setDraft}
        onClose={() => setFullscreenOpen(false)}
        onDiscard={() => setDraft(compose.yamlContent)}
        onSave={() => void save()}
        open={fullscreenOpen}
        project={project}
        saving={isPending(`save-compose:${compose.id}`)}
      />

      <Drawer
        extra={
          <Space>
            <Button
              icon={<CopyOutlined />}
              onClick={() => {
                void navigator.clipboard.writeText(visibleLogLines.join('\n'));
                message.success('Copied visible log lines');
              }}
              size="small"
            >
              Copy
            </Button>
            <Tooltip title="Downloading logs is out of scope for v1 — the API returns the last 200 lines only">
              <Button disabled icon={<CloudDownloadOutlined />} size="small">
                Download
              </Button>
            </Tooltip>
            <Button
              icon={<ReloadOutlined />}
              loading={logsLoading}
              onClick={() => {
                void loadLogs(compose.id)
                  .then(setLogsText)
                  .catch(fail);
              }}
              size="small"
            >
              Refresh
            </Button>
          </Space>
        }
        onClose={() => setLogsOpen(false)}
        open={logsOpen}
        title={
          <Flex align="center" gap={8}>
            <Text strong>Logs</Text>
            <Text type="secondary">
              {project.name} / {environment.name} / {compose.name}
            </Text>
          </Flex>
        }
        width={760}
      >
        <Flex align="center" gap={8} style={{ marginBottom: 12 }} wrap>
          <Select
            maxTagCount="responsive"
            mode="multiple"
            onChange={setLogServices}
            options={services.map((service) => ({ label: service.name, value: service.name }))}
            placeholder="All services"
            style={{ minWidth: 240 }}
            value={logServices}
          />
          <Select
            onChange={setLogLines}
            options={[
              { label: 'Last 50 lines', value: 50 },
              { label: 'Last 200 lines', value: 200 },
              { label: 'Last 500 lines', value: 500 },
            ]}
            style={{ width: 150 }}
            value={logLines}
          />
          <Tooltip title="Live streaming needs a websocket log feed — deliberately out of scope for v1">
            <Flex align="center" gap={6}>
              <Switch disabled size="small" />
              <Text type="secondary">Follow</Text>
            </Flex>
          </Tooltip>
          <Text type="secondary" style={{ marginInlineStart: 'auto' }}>
            {visibleLogLines.length} of {logLineCount} lines
          </Text>
        </Flex>

        {logsLoading ? (
          <Skeleton active paragraph={{ rows: 8 }} title={false} />
        ) : visibleLogLines.length === 0 ? (
          <Empty
            description={<Text type="secondary">No log lines for the selected services.</Text>}
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          />
        ) : (
          <pre
            className="rp-logs"
            style={{
              background: token.colorFillQuaternary,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: token.borderRadius,
              margin: 0,
              maxHeight: 'calc(100vh - 220px)',
              overflow: 'auto',
            }}
          >
            {visibleLogLines.map((line, index) => (
              <div key={`${index}-${line.slice(0, 24)}`}>{line}</div>
            ))}
          </pre>
        )}
        <Title level={5} style={{ marginTop: 16 }}>
          Logs are fetched on demand
        </Title>
        <Text type="secondary">
          The panel returns the last ~200 lines from <Text code>docker compose logs</Text>. Nothing is streamed or stored.
        </Text>
      </Drawer>

      <DangerModal
        confirmWord={compose.name}
        consequences={
          <Paragraph type="secondary">
            This stops and removes containers, networks and volumes created by <Text code>{compose.name}</Text>, then
            deletes its files from{' '}
            <Text code>
              {deployIdentity.directory}
            </Text>
            .
          </Paragraph>
        }
        description="Deleting a Compose tears down everything it created on the host."
        loading={isPending(`delete-compose:${compose.id}`)}
        okText="Delete Compose"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={() => void removeCompose()}
        open={deleteOpen}
        title="Delete this Compose?"
      />
    </>
  );
}
