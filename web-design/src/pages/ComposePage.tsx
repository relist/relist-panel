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
  Badge,
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
  Switch,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import type { MenuProps } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { navigate, paths } from '../app/router';
import { ComposeVolumesTab } from '../components/ComposeVolumesTab';
import { DangerModal } from '../components/DangerModal';
import { FullscreenEditor } from '../components/FullscreenEditor';
import { CodeValue, ComposeStatusTag, HealthBadge, PageHeader, RelativeTime, ServiceStateTag } from '../components/primitives';
import { ServiceMonitor } from '../components/ServiceMonitor';
import { YamlEditor } from '../components/YamlEditor';
import { usePanel } from '../data/store';
import { composeState, findComposeLocation } from '../data/types';
import { useSceneContainer } from '../scenes/scope';

const { Text, Paragraph, Title } = Typography;

type ComposeTab = 'editor' | 'monitor' | 'volumes';

/**
 * Compose detail. Previously: a back button, five action buttons in a row, a
 * fixed-height editor, and logs behind a second tab. Now: lifecycle actions are
 * graded by weight (one primary, one "More"), and the right rail answers "what
 * is running right now, and where does this thing live on the host?" without
 * leaving the screen. The Monitor tab answers the third question — how has this
 * service been behaving — without going back to the host screen.
 *
 * The page scrolls as one document. The editor and the rail stretch to the same
 * height, so the editor is as tall as the status rail next to it (never shorter
 * than `LAYOUT.editorMinHeight`); the rail simply continues below the fold.
 */
export function ComposePage({
  composeId,
  environmentId,
  projectId,
  tab,
}: {
  composeId: number;
  environmentId: number;
  projectId: number;
  tab?: string;
}) {
  const {
    activity,
    checkedAt,
    deleteCompose,
    deployCompose,
    handles,
    isPending,
    loadLogs,
    projects,
    refreshStatus,
    restartCompose,
    saveCompose,
    stopCompose,
  } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const sceneContainer = useSceneContainer();

  const location = findComposeLocation(projects, composeId);
  const compose = location?.compose;
  const environment = location?.environment;
  const project = location?.project;

  const [draft, setDraft] = useState(compose?.yamlContent ?? '');
  const [fullscreenOpen, setFullscreenOpen] = useState(false);
  const [logsOpen, setLogsOpen] = useState(false);
  const [logsText, setLogsText] = useState<string>();
  const [logsLoading, setLogsLoading] = useState(false);
  const [logServices, setLogServices] = useState<string[]>([]);
  const [logLines, setLogLines] = useState(200);
  const [deleteOpen, setDeleteOpen] = useState(false);

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

  // The URL is the source of truth for navigation, so a stale path (renamed
  // parent, hand-edited hash) is redirected to the canonical one — tab included,
  // or the redirect would drop the operator back on the Editor.
  const activeTab: ComposeTab = tab === 'monitor' ? 'monitor' : tab === 'volumes' ? 'volumes' : 'editor';
  const canonicalPath =
    compose && environment && project
      ? paths.compose(project.id, environment.id, compose.id, activeTab === 'editor' ? undefined : activeTab)
      : undefined;
  const pathIsStale = Boolean(canonicalPath) && (projectId !== project?.id || environmentId !== environment?.id);
  useEffect(() => {
    if (pathIsStale && canonicalPath) {
      navigate(canonicalPath);
    }
  }, [canonicalPath, pathIsStale]);

  const dirty = Boolean(compose) && draft !== compose?.yamlContent;

  const logLineList = useMemo(() => (logsText ? logsText.split('\n') : []), [logsText]);
  const visibleLogLines = useMemo(() => {
    const byService =
      logServices.length === 0
        ? logLineList
        : logLineList.filter((line) => logServices.some((service) => line.trimStart().startsWith(service)));
    return byService.slice(-logLines);
  }, [logLineList, logLines, logServices]);

  if (!compose || !environment || !project) {
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
  const checked = checkedAt[compose.id];
  const openHandleCount = Object.values(handles).filter((handle) => handle.composeId === compose.id).length;

  const save = async () => {
    await saveCompose(compose.id, draft);
    message.success('Saved docker-compose.yml');
  };

  const deploy = async () => {
    if (dirty) {
      await saveCompose(compose.id, draft);
    }
    await deployCompose(compose.id);
    message.success(`Deployed ${compose.name}`);
  };

  const openLogs = async () => {
    setLogsOpen(true);
    setLogsLoading(true);
    try {
      setLogsText(await loadLogs(compose.id));
    } finally {
      setLogsLoading(false);
    }
  };

  const moreMenu: MenuProps['items'] = [
    {
      disabled: compose.status.total === 0,
      icon: <RedoOutlined />,
      key: 'restart',
      label: 'Restart containers',
      onClick: () => void restartCompose(compose.id).then(() => message.success(`Restarted ${compose.name}`)),
    },
    {
      disabled: compose.status.total === 0,
      icon: <StopOutlined />,
      key: 'stop',
      label: 'Stop containers',
      onClick: () => void stopCompose(compose.id).then(() => message.success(`Stopped ${compose.name}`)),
    },
    { type: 'divider' },
    { danger: true, icon: <DeleteOutlined />, key: 'delete', label: 'Delete Compose', onClick: () => setDeleteOpen(true) },
  ];

  const relatedActivity = activity.filter((entry) => entry.target === paths.compose(project.id, environment.id, compose.id));

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
            <Button icon={<RocketOutlined />} loading={isPending(`deploy:${compose.id}`)} onClick={() => void deploy()} type="primary">
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
                            <Button loading={isPending(`save-compose:${compose.id}`)} onClick={() => void save()} size="small" type="primary">
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
                  <Flex gap={16} vertical>
                    <Card
                      extra={
                        <Tooltip title="Re-read container status from the host">
                          <Button
                            icon={<ReloadOutlined />}
                            loading={isPending(`status:${compose.id}`)}
                            onClick={() => void refreshStatus(compose.id)}
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

                      {compose.services.length === 0 ? (
                        <Empty
                          description={<Text type="secondary">No containers on the host for this Compose.</Text>}
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          styles={{ image: { height: 40 } }}
                        >
                          <Button icon={<RocketOutlined />} loading={isPending(`deploy:${compose.id}`)} onClick={() => void deploy()} size="small" type="primary">
                            Deploy now
                          </Button>
                        </Empty>
                      ) : (
                        <Flex gap={10} vertical>
                          {compose.services.map((service) => (
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
                        Frozen at Compose creation (ADR-0005). Renaming the Project or Environment does not move what is already
                        deployed.
                      </Paragraph>
                      <Flex gap={10} vertical>
                        <div>
                          <Text strong style={{ fontSize: 12 }}>
                            Files on disk
                          </Text>
                          <CodeValue copyable label="dataDir/" value={`${project.name}/${environment.name}/${compose.name}`} />
                        </div>
                        <div>
                          <Text strong style={{ fontSize: 12 }}>
                            docker compose project
                          </Text>
                          <CodeValue copyable value={`${compose.projectSlug}-${compose.envSlug}-${compose.name}`} />
                        </div>
                      </Flex>
                    </Card>

                    <Card
                      extra={
                        <Button onClick={() => navigate(paths.environment(project.id, environment.id, 'variables'))} size="small" type="link">
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
                                {environment.variables.length} variable{environment.variables.length === 1 ? '' : 's'} applied at deploy time
                              </Text>
                            ),
                            key: 'vars',
                            label: 'Variables',
                          },
                          {
                            children: <Text code style={{ fontSize: 12 }}>{`\${${environment.variables[0]?.key ?? 'KEY'}}`}</Text>,
                            key: 'usage',
                            label: 'Reference as',
                          },
                        ]}
                        size="small"
                      />
                    </Card>

                    <Card title="Activity" variant="outlined">
                      {relatedActivity.length === 0 ? (
                        <Text type="secondary">No recorded actions for this Compose yet.</Text>
                      ) : (
                        <Flex gap={10} vertical>
                          {relatedActivity.slice(0, 5).map((entry) => (
                            <div key={entry.id}>
                              <Text style={{ fontSize: 12 }}>{entry.detail}</Text>
                              <br />
                              <Text type="secondary" style={{ fontSize: 11 }}>
                                <RelativeTime value={entry.at} /> · {entry.actor}
                              </Text>
                            </div>
                          ))}
                        </Flex>
                      )}
                    </Card>
                  </Flex>
                </Col>
              </Row>
            ),
            key: 'editor',
            label: 'Editor',
          },
          {
            children: <ServiceMonitor compose={compose} onOpenEditor={() => navigate(paths.compose(project.id, environment.id, compose.id))} />,
            key: 'monitor',
            label: 'Monitor',
          },
          {
            children: <ComposeVolumesTab compose={compose} environmentId={environment.id} projectId={project.id} />,
            key: 'volumes',
            label: (
              // The badge matters more than it looks: a handle left open is a
              // container still holding a Volume somewhere the operator is not
              // looking, so the count has to be visible from the tab row.
              <Flex align="center" gap={6}>
                Volumes
                {openHandleCount > 0 && <Badge count={openHandleCount} size="small" />}
              </Flex>
            ),
          },
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
        // Portals inside the frame box when scoped, the viewport as today
        // standalone (ticket 10).
        getContainer={sceneContainer}
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
                setLogsLoading(true);
                void loadLogs(compose.id)
                  .then(setLogsText)
                  .finally(() => setLogsLoading(false));
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
            options={compose.services.map((service) => ({ label: service.name, value: service.name }))}
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
            {visibleLogLines.length} of {logLineList.length} lines
          </Text>
        </Flex>

        {logsLoading ? (
          <Skeleton active paragraph={{ rows: 8 }} title={false} />
        ) : visibleLogLines.length === 0 ? (
          <Empty description={<Text type="secondary">No log lines for the selected services.</Text>} image={Empty.PRESENTED_IMAGE_SIMPLE} />
        ) : (
          <pre
            className="rp-logs"
            style={{
              background: token.colorFillQuaternary,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: token.borderRadius,
              margin: 0,
              // Frame-relative inside a scope (`--frame-vh`), viewport-relative
              // as today standalone (ticket 10).
              maxHeight: 'calc(100 * var(--frame-vh, 1vh) - 220px)',
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
            This stops and removes containers, networks and volumes created by <Text code>{compose.name}</Text>, then deletes
            its files from <Text code>dataDir/{project.name}/{environment.name}/{compose.name}/</Text>.
          </Paragraph>
        }
        description="Deleting a Compose tears down everything it created on the host."
        loading={isPending(`delete-compose:${compose.id}`)}
        okText="Delete Compose"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={() => {
          void deleteCompose(compose.id).then(() => {
            setDeleteOpen(false);
            message.success(`Deleted ${compose.name}`);
            navigate(paths.environment(project.id, environment.id));
          });
        }}
        open={deleteOpen}
        title="Delete this Compose?"
      />
    </>
  );
}
