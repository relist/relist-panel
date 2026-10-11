import {
  DeleteOutlined,
  EditOutlined,
  MoreOutlined,
  PlusOutlined,
  RocketOutlined,
  StopOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App,
  Button,
  Card,
  Dropdown,
  Flex,
  Form,
  Input,
  Modal,
  Result,
  Segmented,
  Space,
  Spin,
  Table,
  Tabs,
  Tag,
  Typography,
  theme,
} from 'antd';
import type { MenuProps } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router';

import { paths } from '../app/paths';
import { DangerModal } from '../components/DangerModal';
import { VariablesPanel } from '../components/VariablesPanel';
import { YamlEditor } from '../components/YamlEditor';
import { ComposeStatusInline, ComposeStatusTag, EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { usePanel } from '../data/store';
import type { Compose } from '../data/types';
import { composeState, projectComposes, summarize } from '../data/types';
import { activeTab, selectEnvironment } from './project-view';
import { composeDeployIdentity } from './compose-view';
import { ProjectSettingsTab } from './ProjectSettingsTab';

const { Text, Paragraph } = Typography;

const STARTER_YAML = `services:
  app:
    image: nginx:alpine
    restart: unless-stopped
    labels:
      - traefik.enable=true
      - traefik.http.routers.app.rule=Host(\`app.example.com\`)
      - traefik.http.services.app.loadbalancer.server.port=80
`;

/**
 * Project page (GUIDELINES §4.4). One tab level instead of the monolith's two
 * (Environments as Tabs *and* sections as Tabs nested inside them): the
 * Environment is a `Segmented` switcher, the sections are the tabs, and both
 * live in the URL (`/projects/:p/env/:e/:tab`) so they survive a reload. The
 * Composes and Variables tabs are fully real; Settings is the Project form,
 * the Environment list and the danger zone (`ProjectSettingsTab`).
 */
export function ProjectPage() {
  const { environmentId, projectId: projectParam, tab } = useParams();
  const projectId = Number(projectParam);
  const navigate = useNavigate();
  const {
    addEnvironment,
    createCompose,
    deleteCompose,
    deployCompose,
    isPending,
    loadVariables,
    projects,
    restartCompose,
    stopCompose,
  } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [environmentForm] = Form.useForm<{ name: string }>();
  const [composeForm] = Form.useForm<{ name: string }>();
  const [environmentModalOpen, setEnvironmentModalOpen] = useState(false);
  const [composeModalOpen, setComposeModalOpen] = useState(false);
  const [composeYAML, setComposeYAML] = useState('');
  const [deletingCompose, setDeletingCompose] = useState<Compose>();
  // Action failures stay on this screen as an Alert (GUIDELINES §8) — the row
  // action that retries them is right there.
  const [actionError, setActionError] = useState<string>();
  const fail = (reason: unknown) =>
    setActionError(reason instanceof Error ? reason.message : 'Something went wrong');

  const project = projects.find((candidate) => candidate.id === projectId);
  const environments = project?.environments ?? [];
  const activeEnvironment = project ? selectEnvironment(project, Number(environmentId)) : undefined;
  const activeTabKey = activeTab(tab);

  useEffect(() => {
    setActionError(undefined);
  }, [projectId]);

  // The Variables tab (and the tab count above) read the Environment's
  // variables; the tree ships with them unloaded, so the active Environment's
  // list is fetched here — the panel only renders the wait. A failed load
  // joins this screen's error Alert, per the ticket-07 pattern.
  const variablesEnvironmentId = activeEnvironment?.id;
  useEffect(() => {
    if (variablesEnvironmentId !== undefined) {
      loadVariables(variablesEnvironmentId).catch(fail);
    }
  }, [loadVariables, variablesEnvironmentId]);

  const composes = useMemo(() => (activeEnvironment ? activeEnvironment.composes : []), [activeEnvironment]);

  if (!project) {
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
        title="Project not found"
      />
    );
  }

  const allComposes = projectComposes(project);
  const rollup = summarize(allComposes);

  const goToEnvironment = (id: number, nextTab = activeTabKey) => navigate(paths.environment(project.id, id, nextTab));

  const createEnvironment = async (values: { name: string }) => {
    try {
      await addEnvironment(project.id, values.name);
      setEnvironmentModalOpen(false);
      environmentForm.resetFields();
      message.success(`Created Environment ${values.name}`);
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const submitCompose = async (values: { name: string }) => {
    if (!activeEnvironment) return;
    try {
      const created = await createCompose(activeEnvironment.id, values.name, composeYAML);
      setComposeModalOpen(false);
      composeForm.resetFields();
      setComposeYAML('');
      message.success(`Created ${values.name}`);
      navigate(paths.compose(project.id, activeEnvironment.id, created.id));
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const removeCompose = async () => {
    if (!deletingCompose) return;
    const name = deletingCompose.name;
    try {
      await deleteCompose(deletingCompose.id);
      setDeletingCompose(undefined);
      message.success(`Deleted ${name}`);
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const lifecycle = async (compose: Compose, action: 'deploy' | 'restart' | 'stop') => {
    try {
      if (action === 'deploy') {
        const number = await deployCompose(compose.id, { mode: 'deploy' });
        message.info(`Deployment #${number} queued`);
        return;
      }
      if (action === 'restart') await restartCompose(compose.id);
      if (action === 'stop') await stopCompose(compose.id);
      message.success(`${action === 'restart' ? 'Restarted' : 'Stopped'} ${compose.name}`);
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const rowMenu = (compose: Compose): MenuProps['items'] => {
    const state = composeState(compose.status, compose.lastDeployedAt);
    return [
      {
        icon: <RocketOutlined />,
        key: 'open',
        label: 'Open',
        onClick: () => navigate(paths.compose(project.id, activeEnvironment!.id, compose.id)),
      },
      { type: 'divider' },
      {
        disabled: state === 'running',
        icon: <RocketOutlined />,
        key: 'deploy',
        label: 'Deploy',
        onClick: () => void lifecycle(compose, 'deploy'),
      },
      {
        disabled: compose.status.total === 0,
        icon: <SyncOutlined />,
        key: 'restart',
        label: 'Restart',
        onClick: () => void lifecycle(compose, 'restart'),
      },
      {
        disabled: compose.status.total === 0,
        icon: <StopOutlined />,
        key: 'stop',
        label: 'Stop',
        onClick: () => void lifecycle(compose, 'stop'),
      },
      { type: 'divider' },
      {
        danger: true,
        icon: <DeleteOutlined />,
        key: 'delete',
        label: 'Delete',
        onClick: () => setDeletingCompose(compose),
      },
    ];
  };

  const settingsTab = (
    <ProjectSettingsTab
      activeEnvironmentId={activeEnvironment?.id}
      onNewEnvironment={() => setEnvironmentModalOpen(true)}
      onError={fail}
      project={project}
    />
  );

  return (
    <>
      <PageHeader
        description={project.description || 'No description yet.'}
        extra={
          <>
            <Button
              icon={<EditOutlined />}
              onClick={() => navigate(paths.environment(project.id, activeEnvironment?.id ?? 0, 'settings'))}
            >
              Edit details
            </Button>
            <Button
              icon={<PlusOutlined />}
              onClick={() => setEnvironmentModalOpen(true)}
              type={environments.length === 0 ? 'primary' : 'default'}
            >
              New Environment
            </Button>
          </>
        }
        tags={
          <>
            <ComposeStatusTag
              lastDeployedAt={allComposes.find((compose) => compose.lastDeployedAt)?.lastDeployedAt}
              status={{ running: rollup.running, total: rollup.total }}
            />
            <Tag>{environments.length} Environments</Tag>
            <Tag>{rollup.composes} Composes</Tag>
          </>
        }
        title={project.name}
      />

      {actionError && (
        <Alert
          closable={{ afterClose: () => setActionError(undefined) }}
          title={actionError}
          showIcon
          style={{ marginBottom: 16 }}
          type="error"
        />
      )}

      {environments.length === 0 ? (
        <Card variant="outlined">
          <EmptyState
            action={
              <Button icon={<PlusOutlined />} onClick={() => setEnvironmentModalOpen(true)} type="primary">
                Create the first Environment
              </Button>
            }
            description="An Environment is a deployment target — production, staging, preview. Its Environment Variables are shared by every Compose inside it."
            title="No Environments yet"
          />
        </Card>
      ) : (
        <>
          <Flex align="center" gap={16} justify="space-between" style={{ marginBottom: 16 }} wrap>
            <Flex align="center" gap={12}>
              <Text type="secondary">Environment</Text>
              <Segmented
                onChange={(value) => goToEnvironment(Number(value))}
                options={environments.map((environment) => ({
                  label: (
                    <Flex align="center" gap={6}>
                      {environment.name}
                      <Tag style={{ marginInlineEnd: 0 }}>{environment.composes.length}</Tag>
                    </Flex>
                  ),
                  value: environment.id,
                }))}
                value={activeEnvironment?.id}
              />
            </Flex>
            {activeEnvironment && (
              <Text type="secondary">
                {summarize(activeEnvironment.composes).running}/{summarize(activeEnvironment.composes).total} containers up in{' '}
                {activeEnvironment.name}
              </Text>
            )}
          </Flex>

          <Tabs
            activeKey={activeTabKey}
            items={[
              {
                children: (
                  <>
                    <Flex align="center" justify="space-between" style={{ marginBottom: 16 }} wrap>
                      <Text type="secondary">
                        Each Compose is one <Text code>docker-compose.yml</Text> deployed and stopped on its own.
                      </Text>
                      <Button
                        icon={<PlusOutlined />}
                        onClick={() => {
                          setComposeYAML('');
                          setComposeModalOpen(true);
                        }}
                        type="primary"
                      >
                        New Compose
                      </Button>
                    </Flex>
                    {composes.length === 0 ? (
                      <Card variant="outlined">
                        <EmptyState
                          action={
                            <Button
                              icon={<PlusOutlined />}
                              onClick={() => {
                                setComposeYAML('');
                                setComposeModalOpen(true);
                              }}
                              type="primary"
                            >
                              Write the first Compose
                            </Button>
                          }
                          description="Paste an existing docker-compose.yml, or start from a small Traefik-ready example."
                          title={`No Composes in ${activeEnvironment?.name}`}
                        />
                      </Card>
                    ) : (
                      <Card styles={{ body: { padding: 0 } }} variant="outlined">
                        <Table<Compose>
                          columns={[
                            {
                              dataIndex: 'name',
                              key: 'name',
                              render: (_value, compose) => (
                                <Flex align="center" gap={8}>
                                  <RocketOutlined style={{ color: token.colorTextTertiary }} />
                                  <Text strong>{compose.name}</Text>
                                </Flex>
                              ),
                              title: 'Compose',
                            },
                            {
                              key: 'status',
                              render: (_value, compose) => (
                                <ComposeStatusInline lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
                              ),
                              title: 'Containers',
                              width: 200,
                            },
                            {
                              key: 'identity',
                              render: (_value, compose) => (
                                <Text code style={{ fontSize: 12 }} type="secondary">
                                  {composeDeployIdentity(compose).projectName}
                                </Text>
                              ),
                              title: 'Deploy identity',
                              width: 240,
                            },
                            {
                              dataIndex: 'updatedAt',
                              key: 'updatedAt',
                              render: (value: string) => (
                                <Text type="secondary">
                                  <RelativeTime value={value} />
                                </Text>
                              ),
                              title: 'Last edited',
                              width: 150,
                            },
                            {
                              key: 'actions',
                              // The actions stop the row's click-to-open: a
                              // one-click Deploy/Stop must not navigate away.
                              render: (_value, compose) => (
                                <Space onClick={(event) => event.stopPropagation()} size={4}>
                                  <Button
                                    onClick={() => navigate(paths.compose(project.id, activeEnvironment!.id, compose.id))}
                                    size="small"
                                  >
                                    Open
                                  </Button>
                                  {composeState(compose.status, compose.lastDeployedAt) === 'running' ? (
                                    <Button
                                      icon={<StopOutlined />}
                                      loading={isPending(`stop:${compose.id}`)}
                                      onClick={() => void lifecycle(compose, 'stop')}
                                      size="small"
                                    >
                                      Stop
                                    </Button>
                                  ) : (
                                    <Button
                                      icon={<RocketOutlined />}
                                      loading={isPending(`deploy:${compose.id}`)}
                                      onClick={() => void lifecycle(compose, 'deploy')}
                                      size="small"
                                    >
                                      Deploy
                                    </Button>
                                  )}
                                  <Dropdown menu={{ items: rowMenu(compose) }} placement="bottomRight" trigger={['click']}>
                                    <Button
                                      aria-label={`Actions for ${compose.name}`}
                                      icon={<MoreOutlined />}
                                      size="small"
                                      type="text"
                                    />
                                  </Dropdown>
                                </Space>
                              ),
                              title: '',
                              width: 220,
                            },
                          ]}
                          dataSource={composes}
                          onRow={(compose) => ({
                            className: 'rp-clickable-row',
                            onClick: () => navigate(paths.compose(project.id, activeEnvironment!.id, compose.id)),
                          })}
                          pagination={false}
                          rowKey="id"
                        />
                      </Card>
                    )}
                  </>
                ),
                key: 'composes',
                label: `Composes (${composes.length})`,
              },
              {
                children: activeEnvironment ? (
                  <VariablesPanel environment={activeEnvironment} onError={fail} />
                ) : null,
                key: 'variables',
                label: `Variables (${activeEnvironment?.variables.length ?? 0})`,
              },
              {
                children: settingsTab,
                key: 'settings',
                label: 'Settings',
              },
            ]}
            onChange={(key) => {
              if (!activeEnvironment) return;
              navigate(paths.environment(project.id, activeEnvironment.id, key));
            }}
          />
        </>
      )}

      <Modal
        confirmLoading={isPending(`create-environment:${project.id}`)}
        destroyOnHidden
        okText="Create Environment"
        onCancel={() => setEnvironmentModalOpen(false)}
        onOk={() => environmentForm.submit()}
        open={environmentModalOpen}
        title="New Environment"
      >
        <Form form={environmentForm} layout="vertical" onFinish={(values) => void createEnvironment(values)} requiredMark={false}>
          <Form.Item
            extra="Conventional names: production, staging, preview. Slug-unique within the Project."
            label="Name"
            name="name"
            rules={[
              { message: 'Enter an Environment name', required: true },
              { max: 255, message: 'Use 255 characters or fewer' },
            ]}
          >
            <Input autoFocus placeholder="staging" />
          </Form.Item>
          <Space wrap>
            {['production', 'staging', 'preview'].map((suggestion) => (
              <Tag className="rp-clickable-tag" key={suggestion} onClick={() => environmentForm.setFieldsValue({ name: suggestion })}>
                {suggestion}
              </Tag>
            ))}
          </Space>
        </Form>
      </Modal>

      <Modal
        cancelText="Cancel"
        confirmLoading={isPending(`create-compose:${activeEnvironment?.id ?? 0}`)}
        destroyOnHidden
        okButtonProps={{ disabled: !composeYAML.trim() }}
        okText="Create Compose"
        onCancel={() => setComposeModalOpen(false)}
        onOk={() => composeForm.submit()}
        open={composeModalOpen}
        title={`New Compose in ${activeEnvironment?.name ?? ''}`}
        width={840}
      >
        <Form form={composeForm} layout="vertical" onFinish={(values) => void submitCompose(values)} requiredMark={false}>
          <Form.Item
            label="Name"
            name="name"
            rules={[
              { message: 'Enter a Compose name', required: true },
              { message: 'Use letters, digits, dashes and underscores', pattern: /^[A-Za-z0-9][A-Za-z0-9_-]*$/ },
            ]}
          >
            <Input autoFocus placeholder="web" style={{ maxWidth: 320 }} />
          </Form.Item>
        </Form>
        <Flex align="center" justify="space-between" style={{ marginBottom: 8 }}>
          <Text type="secondary">docker-compose.yml</Text>
          <Button onClick={() => setComposeYAML(STARTER_YAML)} size="small" type="link">
            Insert an example
          </Button>
        </Flex>
        <div style={{ border: `1px solid ${token.colorBorderSecondary}`, borderRadius: token.borderRadius, overflow: 'hidden' }}>
          <YamlEditor height={320} onChange={setComposeYAML} path="new-compose.yml" value={composeYAML} />
        </div>
        <Alert
          title="The panel joins every service to the shared Traefik network on deploy. Routing is yours to declare with labels."
          showIcon
          style={{ marginTop: 12 }}
          type="info"
        />
      </Modal>

      <DangerModal
        confirmWord={deletingCompose?.name ?? ''}
        consequences={
          <Paragraph type="secondary">
            This stops and removes the containers, networks and volumes created by{' '}
            <Text code>{deletingCompose?.name}</Text>, and deletes its files under{' '}
            <Text code>
              {deletingCompose ? composeDeployIdentity(deletingCompose).directory : ''}
            </Text>
            .
          </Paragraph>
        }
        description="Deleting a Compose tears down everything it created on the host."
        loading={isPending(`delete-compose:${deletingCompose?.id ?? 0}`)}
        okText="Delete Compose"
        onCancel={() => setDeletingCompose(undefined)}
        onConfirm={() => void removeCompose()}
        open={Boolean(deletingCompose)}
        title="Delete this Compose?"
      />
    </>
  );
}
