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
  Popconfirm,
  Result,
  Segmented,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  theme,
} from 'antd';
import type { MenuProps } from 'antd';
import { useEffect, useLayoutEffect, useMemo } from 'react';
import { navigate, paths } from '../app/router';
import { publishSceneHandle } from '../app/sceneAdapters';
import type { SceneForm } from '../app/sceneAdapters';
import { useUiState } from '../app/uiState';
import { DangerModal } from '../components/DangerModal';
import { ComposeStatusInline, ComposeStatusTag, EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { VariablesPanel } from '../components/VariablesPanel';
import { YamlEditor } from '../components/YamlEditor';
import { usePanel } from '../data/store';
import type { Compose, Environment, Project } from '../data/types';
import { composeState, projectComposes, summarize } from '../data/types';
import { useSceneContainer } from '../scenes/scope';

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

type TabKey = 'composes' | 'settings' | 'variables';

/**
 * One Compose row's ⋯ menu. Ticket 02, decision D2: per-row state is addressed
 * by naming the row in the key (`?ui=project.rowMenu.web=1` opens exactly web's
 * menu), so the controlled `open` lives here, once per rendered row. With no
 * query string the state starts closed and the click opens it exactly as the
 * uncontrolled `trigger` did.
 */
function ComposeRowMenu({ compose, menu }: { compose: Compose; menu: MenuProps['items'] }) {
  const [open, setOpen] = useUiState(`project.rowMenu.${compose.name}`, false);
  return (
    <Dropdown menu={{ items: menu }} onOpenChange={setOpen} open={open} placement="bottomRight" trigger={['click']}>
      <Button icon={<MoreOutlined />} size="small" type="text" />
    </Dropdown>
  );
}

/**
 * One row of the Environments table's delete confirm (audit §5.5's per-row
 * `Popconfirm`, addressed as `?ui=project.deleteEnv.staging=1`). Confirming
 * stays the caller's real `onConfirm`; only the open state is addressable.
 */
function DeleteEnvironmentButton({
  environment,
  loading,
  onDelete,
}: {
  environment: Environment;
  loading: boolean;
  onDelete: () => void;
}) {
  const [open, setOpen] = useUiState(`project.deleteEnv.${environment.name}`, false);
  return (
    <Popconfirm
      cancelText="Keep"
      description={`This stops and removes every Compose in it (${environment.composes.length}).`}
      okButtonProps={{ danger: true }}
      okText="Delete"
      onConfirm={onDelete}
      onOpenChange={setOpen}
      open={open}
      title={`Delete ${environment.name}?`}
    >
      <Button danger icon={<DeleteOutlined />} loading={loading} size="small" type="text" />
    </Popconfirm>
  );
}

/**
 * Project page. One tab level instead of the previous two (Environments as
 * Tabs *and* Overview/Variables/Composes as Tabs nested inside them): the
 * Environment is a switcher, the sections are the tabs. Which Environment you
 * are looking at stays visible while you move between sections.
 */
export function ProjectPage({ environmentId, projectId, tab }: { environmentId?: number; projectId: number; tab?: string }) {
  const { addEnvironment, createCompose, deleteCompose, deleteEnvironment, deleteProject, deployCompose, isPending, projects, restartCompose, stopCompose, updateProject } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [projectForm] = Form.useForm<{ name: string; description: string }>();
  const [environmentForm] = Form.useForm<{ name: string }>();
  const [composeForm] = Form.useForm<{ name: string }>();
  const [environmentModalOpen, setEnvironmentModalOpen] = useUiState('project.environmentModalOpen', false);
  const [composeModalOpen, setComposeModalOpen] = useUiState('project.composeModalOpen', false);
  // `composeYAML` is a whole YAML document (audit §5.10) — a `?ui=` literal
  // cannot carry it, so the `fixture:project.composeYAML` adapter seeds
  // `STARTER_YAML` through this screen's own setter. `deletingCompose` holds a
  // `Compose` (decision D1) — seeded via the `object:` adapter, likewise.
  const [composeYAML, setComposeYAML] = useUiState('project.composeYAML', '');
  const [deletingCompose, setDeletingCompose] = useUiState<Compose | undefined>('project.deletingCompose', undefined);
  const [deleteProjectOpen, setDeleteProjectOpen] = useUiState('project.deleteProjectOpen', false);
  const sceneContainer = useSceneContainer();

  const project = projects.find((candidate) => candidate.id === projectId);
  const environments = project?.environments ?? [];
  const activeEnvironment = environments.find((candidate) => candidate.id === environmentId) ?? environments[0];

  const activeTab: TabKey = tab === 'variables' || tab === 'settings' ? tab : 'composes';

  useEffect(() => {
    if (project) {
      projectForm.setFieldsValue({ description: project.description, name: project.name });
    }
  }, [project, projectForm]);

  const composes = useMemo(() => (activeEnvironment ? activeEnvironment.composes : []), [activeEnvironment]);

  // The DangerModals' own confirms, with the target explicit so the scene
  // harness can invoke them for a seeded state (the OK click passes the state
  // through). Both stay exactly what they were as inline handlers.
  const confirmDeleteCompose = (target: Compose) => {
    const name = target.name;
    void deleteCompose(target.id).then(() => {
      setDeletingCompose(undefined);
      message.success(`Deleted ${name}`);
    });
  };

  const confirmDeleteProject = (target: Project) => {
    void deleteProject(target.id).then(() => {
      setDeleteProjectOpen(false);
      message.success(`Deleted ${target.name}`);
      navigate(paths.projects);
    });
  };

  // Ticket 02: the scene adapters reach this screen's own transitions through
  // these handles. Each is idempotent — a seed finds its target already set, a
  // submit replays onto the same form.
  useLayoutEffect(
    () =>
      publishSceneHandle<SceneForm>('project.environment.form', {
        setFieldsValue: (values) => environmentForm.setFieldsValue(values as { name: string }),
        submit: () => environmentForm.submit(),
      }),
    [environmentForm],
  );

  useLayoutEffect(
    () =>
      publishSceneHandle<SceneForm>('project.compose.form', {
        setFieldsValue: (values) => composeForm.setFieldsValue(values as { name: string }),
        submit: () => composeForm.submit(),
      }),
    [composeForm],
  );

  // The Settings form needs no values: the screen's own effect prefills it from
  // the project, so a submit is the real "Saved Project details" transition.
  useLayoutEffect(
    () =>
      publishSceneHandle<SceneForm>('project.settings.form', {
        setFieldsValue: (values) => projectForm.setFieldsValue(values as { description: string; name: string }),
        submit: () => projectForm.submit(),
      }),
    [projectForm],
  );

  // §5.10: the New Compose YAML is a whole document, seeded as the screen's own
  // "Insert an example" hands it over.
  useLayoutEffect(
    () => publishSceneHandle('project.composeYAML.fixture', () => setComposeYAML(STARTER_YAML)),
    [setComposeYAML],
  );

  useLayoutEffect(
    () =>
      publishSceneHandle('project.deletingCompose.seed', () => {
        const target = composes.find((candidate) => candidate.name === 'web') ?? composes[0];
        setDeletingCompose((current) => current ?? target);
      }),
    [composes, setDeletingCompose],
  );

  // The typed-name flow completed (audit §3b): open the confirm seeded, then
  // invoke the real handler. The typed state itself is `danger.typed`.
  useLayoutEffect(
    () =>
      publishSceneHandle('project.deleteCompose.flow', () => {
        const target = composes.find((candidate) => candidate.name === 'web') ?? composes[0];
        if (target) {
          setDeletingCompose(target);
          confirmDeleteCompose(target);
        }
      }),
    [composes, confirmDeleteCompose, setDeletingCompose],
  );

  // Scene `project-zero-envs`: project 4 ("blog") has one Environment, and a
  // Project with none is exactly its absence — removed through the store's own
  // deleteEnvironment, readiness waiting for the delete to settle.
  useLayoutEffect(
    () =>
      publishSceneHandle('project.zeroEnvs.seed', () => {
        if (!project) return;
        for (const environment of environments) {
          void deleteEnvironment(project.id, environment.id);
        }
      }),
    [deleteEnvironment, environments, project],
  );

  if (!project) {
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

  const rollup = summarize(projectComposes(project));

  const goToEnvironment = (id: number, nextTab: TabKey = activeTab) => navigate(paths.environment(project.id, id, nextTab));

  const createEnvironment = async (values: { name: string }) => {
    await addEnvironment(project.id, values.name);
    setEnvironmentModalOpen(false);
    environmentForm.resetFields();
    message.success(`Created Environment ${values.name}`);
  };

  const submitCompose = async (values: { name: string }) => {
    if (!activeEnvironment) return;
    const created = await createCompose(activeEnvironment.id, values.name, composeYAML);
    setComposeModalOpen(false);
    composeForm.resetFields();
    setComposeYAML('');
    message.success(`Created ${values.name}`);
    navigate(paths.compose(project.id, activeEnvironment.id, created.id));
  };

  const lifecycle = async (compose: Compose, action: 'deploy' | 'restart' | 'stop') => {
    if (action === 'deploy') await deployCompose(compose.id);
    if (action === 'restart') await restartCompose(compose.id);
    if (action === 'stop') await stopCompose(compose.id);
    message.success(`${action === 'deploy' ? 'Deployed' : action === 'restart' ? 'Restarted' : 'Stopped'} ${compose.name}`);
  };

  const rowMenu = (compose: Compose): MenuProps['items'] => {
    const state = composeState(compose.status, compose.lastDeployedAt);
    return [
      { icon: <RocketOutlined />, key: 'open', label: 'Open', onClick: () => navigate(paths.compose(project.id, activeEnvironment!.id, compose.id)) },
      { type: 'divider' },
      { disabled: state === 'running', icon: <RocketOutlined />, key: 'deploy', label: 'Deploy', onClick: () => void lifecycle(compose, 'deploy') },
      { disabled: compose.status.total === 0, icon: <SyncOutlined />, key: 'restart', label: 'Restart', onClick: () => void lifecycle(compose, 'restart') },
      { disabled: compose.status.total === 0, icon: <StopOutlined />, key: 'stop', label: 'Stop', onClick: () => void lifecycle(compose, 'stop') },
      { type: 'divider' },
      { danger: true, icon: <DeleteOutlined />, key: 'delete', label: 'Delete', onClick: () => setDeletingCompose(compose) },
    ];
  };

  return (
    <>
      <PageHeader
        description={project.description || 'No description yet.'}
        extra={
          <>
            <Button icon={<EditOutlined />} onClick={() => navigate(paths.environment(project.id, activeEnvironment?.id ?? 0, 'settings'))}>
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
              lastDeployedAt={projectComposes(project).find((compose) => compose.lastDeployedAt)?.lastDeployedAt}
              status={{ running: rollup.running, total: rollup.total }}
            />
            <Tag>{environments.length} Environments</Tag>
            <Tag>{rollup.composes} Composes</Tag>
          </>
        }
        title={project.name}
      />

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
            activeKey={activeTab}
            items={[
              {
                children: (
                  <>
                    <Flex align="center" justify="space-between" style={{ marginBottom: 16 }} wrap>
                      <Text type="secondary">
                        Each Compose is one <Text code>docker-compose.yml</Text> deployed and stopped on its own.
                      </Text>
                      <Button icon={<PlusOutlined />} onClick={() => { setComposeYAML(''); setComposeModalOpen(true); }} type="primary">
                        New Compose
                      </Button>
                    </Flex>
                    {composes.length === 0 ? (
                      <Card variant="outlined">
                        <EmptyState
                          action={
                            <Button icon={<PlusOutlined />} onClick={() => { setComposeYAML(''); setComposeModalOpen(true); }} type="primary">
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
                                  {compose.projectSlug}-{compose.envSlug}-{compose.name}
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
                              render: (_value, compose) => (
                                <Space size={4}>
                                  <Button onClick={() => navigate(paths.compose(project.id, activeEnvironment!.id, compose.id))} size="small">
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
                                  <ComposeRowMenu compose={compose} menu={rowMenu(compose)} />
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
                  <VariablesPanel environment={activeEnvironment} />
                ) : null,
                key: 'variables',
                label: `Variables (${activeEnvironment?.variables.length ?? 0})`,
              },
              {
                children: (
                  <Flex gap={16} vertical>
                    <Card title="Project details" variant="outlined">
                      <Form
                        form={projectForm}
                        layout="vertical"
                        onFinish={(values) => {
                          void updateProject(project.id, values).then(() => message.success('Saved Project details'));
                        }}
                        requiredMark={false}
                      >
                        <Form.Item
                          extra="Renaming does not move already-deployed stacks — deploy identity is frozen at Compose creation (ADR-0005)."
                          label="Name"
                          name="name"
                          rules={[{ message: 'Enter a Project name', required: true }]}
                        >
                          <Input style={{ maxWidth: 360 }} />
                        </Form.Item>
                        <Form.Item label="Description" name="description">
                          <Input.TextArea rows={3} style={{ maxWidth: 560 }} />
                        </Form.Item>
                        <Button htmlType="submit" loading={isPending(`save-project:${project.id}`)} type="primary">
                          Save changes
                        </Button>
                      </Form>
                    </Card>

                    <Card title="Environments" variant="outlined">
                      <Table
                        columns={[
                          { dataIndex: 'name', key: 'name', title: 'Environment' },
                          {
                            key: 'composes',
                            render: (_value, environment) => <Tag>{environment.composes.length} Composes</Tag>,
                            title: '',
                            width: 160,
                          },
                          {
                            key: 'created',
                            render: (_value, environment) => (
                              <Text type="secondary">
                                Created <RelativeTime value={environment.createdAt} />
                              </Text>
                            ),
                            title: 'Created',
                            width: 200,
                          },
                          {
                            key: 'actions',
                            render: (_value, environment) => (
                              <DeleteEnvironmentButton
                                environment={environment}
                                loading={isPending(`delete-environment:${environment.id}`)}
                                onDelete={() => {
                                  void deleteEnvironment(project.id, environment.id).then(() => {
                                    message.success(`Deleted Environment ${environment.name}`);
                                    if (environment.id === activeEnvironment?.id) {
                                      navigate(paths.project(project.id));
                                    }
                                  });
                                }}
                              />
                            ),
                            title: '',
                            width: 70,
                          },
                        ]}
                        dataSource={environments}
                        pagination={false}
                        rowKey="id"
                        size="small"
                      />
                    </Card>

                    <Card
                      style={{ borderColor: token.colorErrorBorder }}
                      title={<Text type="danger">Danger zone</Text>}
                      variant="outlined"
                    >
                      <Flex align="center" gap={16} justify="space-between" wrap>
                        <div>
                          <Text strong style={{ display: 'block' }}>
                            Delete this Project
                          </Text>
                          <Text type="secondary">
                            Stops and removes all {rollup.composes} Composes across {environments.length} Environments, including volumes and files on disk.
                          </Text>
                        </div>
                        <Button danger icon={<DeleteOutlined />} onClick={() => setDeleteProjectOpen(true)}>
                          Delete Project
                        </Button>
                      </Flex>
                    </Card>
                  </Flex>
                ),
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
        getContainer={sceneContainer}
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
            rules={[{ message: 'Enter an Environment name', required: true }]}
          >
            <Input autoFocus placeholder="staging" />
          </Form.Item>
          <Space wrap>
            {['production', 'staging', 'preview'].map((suggestion) => (
              <Tag
                className="rp-clickable-tag"
                key={suggestion}
                onClick={() => environmentForm.setFieldsValue({ name: suggestion })}
              >
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
        getContainer={sceneContainer}
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
          message="The panel joins every service to the shared Traefik network on deploy. Routing is yours to declare with labels."
          showIcon
          style={{ marginTop: 12 }}
          type="info"
        />
      </Modal>

      <DangerModal
        confirmWord={deletingCompose?.name ?? ''}
        consequences={
          <Paragraph type="secondary">
            This stops and removes the containers, networks and volumes created by <Text code>{deletingCompose?.name}</Text>, and deletes its files
            under <Text code>{deletingCompose?.projectSlug}/{deletingCompose?.envSlug}/{deletingCompose?.name}</Text>.
          </Paragraph>
        }
        description="Deleting a Compose tears down everything it created on the host."
        loading={isPending(`delete-compose:${deletingCompose?.id ?? 0}`)}
        okText="Delete Compose"
        onCancel={() => setDeletingCompose(undefined)}
        onConfirm={() => {
          if (deletingCompose) confirmDeleteCompose(deletingCompose);
        }}
        open={Boolean(deletingCompose)}
        title="Delete this Compose?"
      />

      <DangerModal
        confirmWord={project.name}
        consequences={
          <Paragraph type="secondary">
            Every Environment, Compose, container, network and volume under <Text code>{project.name}</Text> is removed from the host.
          </Paragraph>
        }
        description="Deleting a Project cascades to everything inside it."
        loading={isPending(`delete-project:${project.id}`)}
        okText="Delete Project"
        onCancel={() => setDeleteProjectOpen(false)}
        onConfirm={() => confirmDeleteProject(project)}
        open={deleteProjectOpen}
        title="Delete this Project?"
      />
    </>
  );
}
