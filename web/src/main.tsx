import { StyleProvider } from '@ant-design/cssinjs';
import Editor from '@monaco-editor/react';
import {
  Alert,
  Button,
  Card,
  Col,
  ConfigProvider,
  Empty,
  Form,
  Input,
  Layout,
  Menu,
  Modal,
  Popconfirm,
  Row,
  Space,
  Spin,
  Table,
  Tabs,
  Tag,
  Typography,
  theme,
} from 'antd';
import { StrictMode, useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import './monaco';

const codeFontFamily = "'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace";

type AuthMode = 'setup' | 'login';
type Screen = AuthMode | 'authenticated' | 'loading' | 'error';

type Credentials = {
  email: string;
  password: string;
  confirmPassword?: string;
};

type AuthStatus = {
  setupRequired: boolean;
  authenticated: boolean;
};

type Project = {
  id: number;
  name: string;
  description: string;
  environmentCount: number;
  createdAt: string;
  lastUpdated: string;
};

type ProjectInput = {
  name: string;
  description: string;
};

type Environment = {
  id: number;
  projectId: number;
  name: string;
  createdAt: string;
};

type EnvironmentInput = {
  name: string;
};

type EnvironmentVariable = {
  id: number;
  environmentId: number;
  key: string;
  value: string;
};

type EnvironmentVariableInput = {
  key: string;
  value: string;
};

type ComposeStatus = {
  running: number;
  total: number;
};

type Compose = {
  id: number;
  environmentId: number;
  name: string;
  yamlContent: string;
  createdAt: string;
  updatedAt: string;
  status: ComposeStatus;
};

type ComposeCreateInput = {
  name: string;
  yamlContent: string;
};

type ErrorResponse = {
  error?: string;
};

async function getAuthStatus(): Promise<AuthStatus> {
  const response = await fetch('/api/auth/status', { credentials: 'same-origin' });
  if (!response.ok) {
    throw new Error(await responseError(response));
  }
  return response.json() as Promise<AuthStatus>;
}

async function responseError(response: Response): Promise<string> {
  try {
    const payload = (await response.json()) as ErrorResponse;
    if (payload.error) {
      return payload.error;
    }
  } catch {
    // Fall through to a status-based message when the response is not JSON.
  }
  return `Request failed (${response.status})`;
}

function App() {
  const [screen, setScreen] = useState<Screen>('loading');
  const [notice, setNotice] = useState<string>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    void getAuthStatus()
      .then((status) => {
        if (!active) return;
        setScreen(status.authenticated ? 'authenticated' : status.setupRequired ? 'setup' : 'login');
      })
      .catch((reason: unknown) => {
        if (!active) return;
        setError(reason instanceof Error ? reason.message : 'Could not contact the panel');
        setScreen('error');
      });

    return () => {
      active = false;
    };
  }, []);

  const submitCredentials = async (mode: AuthMode, values: Credentials) => {
    setError(undefined);
    const response = await fetch(`/api/auth/${mode}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ email: values.email, password: values.password }),
    });
    if (!response.ok) {
      throw new Error(await responseError(response));
    }

    if (mode === 'setup') {
      setNotice('Admin account created. Sign in to continue.');
      setScreen('login');
      return;
    }
    setNotice(undefined);
    setScreen('authenticated');
  };

  const logout = async () => {
    setError(undefined);
    const response = await fetch('/api/auth/logout', {
      method: 'POST',
      credentials: 'same-origin',
    });
    if (!response.ok) {
      throw new Error(await responseError(response));
    }
    setScreen('login');
    setNotice(undefined);
  };

  if (screen === 'loading') {
    return <LoadingScreen />;
  }

  if (screen === 'error') {
    return <ErrorScreen message={error ?? 'Could not contact the panel'} />;
  }

  if (screen === 'authenticated') {
    return <AuthenticatedScreen onLogout={() => void logout().catch((reason: unknown) => setError(errorMessage(reason)))} error={error} />;
  }

  return (
    <AuthScreen
      mode={screen}
      notice={notice}
      error={error}
      onSubmit={(values) => submitCredentials(screen, values).catch((reason: unknown) => setError(errorMessage(reason)))}
    />
  );
}

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : 'Something went wrong';
}

function LoadingScreen() {
  return (
    <Layout className="min-h-screen items-center justify-center bg-[#f5f5f5]">
      <Spin size="large" aria-label="Loading" />
    </Layout>
  );
}

function ErrorScreen({ message }: { message: string }) {
  return (
    <Layout className="min-h-screen items-center justify-center bg-[#f5f5f5] px-4">
      <Card className="w-full max-w-md">
        <Typography.Title level={3}>Relist Panel</Typography.Title>
        <Alert type="error" showIcon message={message} />
      </Card>
    </Layout>
  );
}

function AuthScreen({
  mode,
  notice,
  error,
  onSubmit,
}: {
  mode: AuthMode;
  notice?: string;
  error?: string;
  onSubmit: (values: Credentials) => Promise<void>;
}) {
  const [form] = Form.useForm<Credentials>();
  const [submitting, setSubmitting] = useState(false);
  const setup = mode === 'setup';

  const submit = async (values: Credentials) => {
    setSubmitting(true);
    try {
      await onSubmit(values);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Layout className="min-h-screen bg-[#f5f5f5]">
      <Layout.Header className="!flex !h-16 !items-center !border-b !border-[#f0f0f0] !bg-white !px-6">
        <Typography.Text className="!text-[#1f1f1f]" strong>
          Relist Panel
        </Typography.Text>
      </Layout.Header>
      <Layout.Content className="flex items-center justify-center px-4 py-12">
        <Card className="w-full max-w-[440px]" bordered>
          <div className="mb-8">
            <Typography.Title className="!mb-2" level={2}>
              {setup ? 'Set up your admin account' : 'Sign in to Relist Panel'}
            </Typography.Title>
            <Typography.Text type="secondary">
              {setup
                ? 'Create the account used to manage this host.'
                : 'Use your admin account to continue.'}
            </Typography.Text>
          </div>

          {notice && <Alert className="mb-6" type="success" showIcon message={notice} />}
          {error && <Alert className="mb-6" type="error" showIcon message={error} />}

          <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} requiredMark={false}>
            <Form.Item
              label="Email"
              name="email"
              rules={[
                { required: true, message: 'Enter your email address' },
                { type: 'email', message: 'Enter a valid email address' },
              ]}
            >
              <Input size="large" autoComplete="email" autoFocus />
            </Form.Item>
            <Form.Item
              label="Password"
              name="password"
              rules={[
                { required: true, message: 'Enter your password' },
                ...(setup ? [{ min: 8, message: 'Use at least 8 characters' }] : []),
              ]}
            >
              <Input.Password size="large" autoComplete={setup ? 'new-password' : 'current-password'} />
            </Form.Item>
            {setup && (
              <Form.Item
                label="Confirm password"
                name="confirmPassword"
                dependencies={['password']}
                rules={[
                  { required: true, message: 'Confirm your password' },
                  ({ getFieldValue }) => ({
                    validator(_, value) {
                      if (!value || getFieldValue('password') === value) {
                        return Promise.resolve();
                      }
                      return Promise.reject(new Error('Passwords do not match'));
                    },
                  }),
                ]}
              >
                <Input.Password size="large" autoComplete="new-password" />
              </Form.Item>
            )}
            <Button block htmlType="submit" loading={submitting} size="large" type="primary">
              {setup ? 'Create admin account' : 'Sign in'}
            </Button>
          </Form>
        </Card>
      </Layout.Content>
    </Layout>
  );
}

function AuthenticatedScreen({ onLogout, error }: { onLogout: () => void; error?: string }) {
  const [selectedProjectID, setSelectedProjectID] = useState<number>();
  const { token } = theme.useToken();

  return (
    <Layout className="min-h-screen" style={{ background: token.colorBgLayout }}>
      <Layout.Sider
        breakpoint="lg"
        collapsedWidth={0}
        theme="light"
        style={{ background: token.colorBgContainer, borderInlineEnd: `1px solid ${token.colorBorderSecondary}` }}
      >
        <div
          className="flex h-16 items-center px-6"
          style={{ borderBottom: `1px solid ${token.colorBorderSecondary}` }}
        >
          <Typography.Text strong>
            Relist Panel
          </Typography.Text>
        </div>
        <Menu
          style={{ borderInlineEnd: 0, paddingTop: token.padding }}
          selectedKeys={selectedProjectID ? [] : ['projects']}
          items={[{ key: 'projects', label: 'Projects' }]}
          onClick={() => setSelectedProjectID(undefined)}
        />
      </Layout.Sider>
      <Layout>
        <Layout.Header
          className="!flex !h-16 !items-center !justify-between !px-6"
          style={{ background: token.colorBgContainer, borderBottom: `1px solid ${token.colorBorderSecondary}` }}
        >
          <Typography.Title className="!mb-0" level={4}>
            {selectedProjectID ? 'Project' : 'Projects'}
          </Typography.Title>
          <Button onClick={onLogout}>Log out</Button>
        </Layout.Header>
        <Layout.Content className="px-6 py-8">
          <div className="mx-auto w-full max-w-6xl">
            {error && <Alert className="mb-6" type="error" showIcon message={error} />}
            {selectedProjectID ? (
              <ProjectDetailScreen projectID={selectedProjectID} onBack={() => setSelectedProjectID(undefined)} />
            ) : (
              <ProjectsScreen onOpenProject={setSelectedProjectID} />
            )}
          </div>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}

function ProjectsScreen({ onOpenProject }: { onOpenProject: (id: number) => void }) {
  const [form] = Form.useForm<ProjectInput>();
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [deletingID, setDeletingID] = useState<number>();
  const [modalOpen, setModalOpen] = useState(false);
  const [editingProject, setEditingProject] = useState<Project>();
  const [error, setError] = useState<string>();

  const loadProjects = async () => {
    setLoading(true);
    setError(undefined);
    try {
      const response = await fetch('/api/projects', { credentials: 'same-origin' });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      setProjects((await response.json()) as Project[]);
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadProjects();
  }, []);

  const openCreateModal = () => {
    setEditingProject(undefined);
    form.resetFields();
    setModalOpen(true);
  };

  const openEditModal = (project: Project) => {
    setEditingProject(project);
    form.setFieldsValue({ name: project.name, description: project.description });
    setModalOpen(true);
  };

  const closeModal = () => {
    if (!saving) {
      setModalOpen(false);
      form.resetFields();
    }
  };

  const saveProject = async (values: ProjectInput) => {
    setSaving(true);
    setError(undefined);
    try {
      const response = await fetch(editingProject ? `/api/projects/${editingProject.id}` : '/api/projects', {
        method: editingProject ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(values),
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      setModalOpen(false);
      form.resetFields();
      await loadProjects();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };

  const deleteProject = async (id: number) => {
    setDeletingID(id);
    setError(undefined);
    try {
      const response = await fetch(`/api/projects/${id}`, {
        method: 'DELETE',
        credentials: 'same-origin',
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      await loadProjects();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setDeletingID(undefined);
    }
  };

  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <Typography.Title className="!mb-1" level={2}>
            Projects
          </Typography.Title>
          <Typography.Text type="secondary">Group the Environments that belong to each application.</Typography.Text>
        </div>
        <Button type="primary" onClick={openCreateModal}>
          New Project
        </Button>
      </div>

      {error && (
        <Alert
          className="mb-6"
          type="error"
          showIcon
          message={error}
          action={<Button onClick={() => void loadProjects()}>Retry</Button>}
        />
      )}

      <Spin spinning={loading}>
        {projects.length === 0 && !loading ? (
          <Card>
            <Empty description="No Projects yet">
              <Button type="primary" onClick={openCreateModal}>
                Create your first Project
              </Button>
            </Empty>
          </Card>
        ) : (
          <Row gutter={[16, 16]}>
            {projects.map((project) => (
              <Col key={project.id} xs={24} md={12} xl={8}>
                <Card
                  title={<Typography.Text strong>{project.name}</Typography.Text>}
                  actions={[
                    <Button key="open" type="link" onClick={() => onOpenProject(project.id)}>
                      Open
                    </Button>,
                    <Button key="edit" type="link" onClick={() => openEditModal(project)}>
                      Edit
                    </Button>,
                    <Popconfirm
                      key="delete"
                      title="Delete this Project?"
                      description="This action cannot be undone."
                      okText="Delete"
                      okButtonProps={{ danger: true }}
                      onConfirm={() => void deleteProject(project.id)}
                    >
                      <Button type="link" danger loading={deletingID === project.id}>
                        Delete
                      </Button>
                    </Popconfirm>,
                  ]}
                >
                  <Typography.Paragraph type={project.description ? undefined : 'secondary'} ellipsis={{ rows: 3 }}>
                    {project.description || 'No description provided.'}
                  </Typography.Paragraph>
                  <Space direction="vertical" size={4}>
                    <Typography.Text type="secondary">
                      Environments: {project.environmentCount}
                    </Typography.Text>
                    <Typography.Text type="secondary">
                      Last updated: {formatProjectDate(project.lastUpdated)}
                    </Typography.Text>
                  </Space>
                </Card>
              </Col>
            ))}
          </Row>
        )}
      </Spin>

      <Modal
        title={editingProject ? 'Edit Project' : 'New Project'}
        open={modalOpen}
        okText={editingProject ? 'Save changes' : 'Create Project'}
        confirmLoading={saving}
        onCancel={closeModal}
        onOk={() => form.submit()}
        destroyOnClose
      >
        <Form form={form} layout="vertical" requiredMark={false} onFinish={(values) => void saveProject(values)}>
          <Form.Item
            label="Name"
            name="name"
            rules={[
              { required: true, message: 'Enter a Project name' },
              { max: 255, message: 'Use 255 characters or fewer' },
            ]}
          >
            <Input autoFocus placeholder="e.g. my-saas" />
          </Form.Item>
          <Form.Item label="Description" name="description">
            <Input.TextArea rows={4} placeholder="What does this Project contain?" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}

function ProjectDetailScreen({ projectID, onBack }: { projectID: number; onBack: () => void }) {
  const [environmentForm] = Form.useForm<EnvironmentInput>();
  const [project, setProject] = useState<Project>();
  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [activeEnvironmentID, setActiveEnvironmentID] = useState<number>();
  const [loading, setLoading] = useState(true);
  const [savingEnvironment, setSavingEnvironment] = useState(false);
  const [deletingEnvironmentID, setDeletingEnvironmentID] = useState<number>();
  const [environmentModalOpen, setEnvironmentModalOpen] = useState(false);
  const [error, setError] = useState<string>();
  const detailRequestID = useRef(0);

  const loadDetail = async (preferredEnvironmentID?: number) => {
    const requestID = ++detailRequestID.current;
    setLoading(true);
    setError(undefined);
    try {
      const [projectResponse, environmentsResponse] = await Promise.all([
        fetch(`/api/projects/${projectID}`, { credentials: 'same-origin' }),
        fetch(`/api/projects/${projectID}/environments`, { credentials: 'same-origin' }),
      ]);
      if (!projectResponse.ok) {
        throw new Error(await responseError(projectResponse));
      }
      if (!environmentsResponse.ok) {
        throw new Error(await responseError(environmentsResponse));
      }

      const nextProject = (await projectResponse.json()) as Project;
      const nextEnvironments = (await environmentsResponse.json()) as Environment[];
      if (requestID !== detailRequestID.current) {
        return;
      }
      setProject(nextProject);
      setEnvironments(nextEnvironments);
      setActiveEnvironmentID(selectEnvironmentID(nextEnvironments, preferredEnvironmentID));
    } catch (reason: unknown) {
      if (requestID === detailRequestID.current) {
        setError(errorMessage(reason));
      }
    } finally {
      if (requestID === detailRequestID.current) {
        setLoading(false);
      }
    }
  };

  useEffect(() => {
    void loadDetail();
    return () => {
      detailRequestID.current += 1;
    };
  }, [projectID]);

  const openEnvironmentModal = () => {
    environmentForm.resetFields();
    setEnvironmentModalOpen(true);
  };

  const closeEnvironmentModal = () => {
    if (!savingEnvironment) {
      setEnvironmentModalOpen(false);
      environmentForm.resetFields();
    }
  };

  const createEnvironment = async (values: EnvironmentInput) => {
    setSavingEnvironment(true);
    setError(undefined);
    try {
      const response = await fetch(`/api/projects/${projectID}/environments`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(values),
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const created = (await response.json()) as Environment;
      setEnvironmentModalOpen(false);
      environmentForm.resetFields();
      await loadDetail(created.id);
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSavingEnvironment(false);
    }
  };

  const deleteEnvironment = async (environmentID: number) => {
    setDeletingEnvironmentID(environmentID);
    setError(undefined);
    try {
      const response = await fetch(`/api/projects/${projectID}/environments/${environmentID}`, {
        method: 'DELETE',
        credentials: 'same-origin',
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      await loadDetail(activeEnvironmentID === environmentID ? undefined : activeEnvironmentID);
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setDeletingEnvironmentID(undefined);
    }
  };

  if (loading) {
    return (
      <Card>
        <div className="flex justify-center py-12">
          <Spin size="large" />
        </div>
      </Card>
    );
  }

  if (error || !project) {
    return (
      <Card>
        <Button className="!mb-6" onClick={onBack}>
          Back to Projects
        </Button>
        <Alert type="error" showIcon message={error ?? 'Project not found'} />
      </Card>
    );
  }

  return (
    <>
      <Button className="!mb-6" onClick={onBack}>
        Back to Projects
      </Button>
      {error && <Alert className="mb-6" type="error" showIcon message={error} />}
      <Card title={project.name}>
        <Typography.Paragraph type={project.description ? undefined : 'secondary'}>
          {project.description || 'No description provided.'}
        </Typography.Paragraph>
        <Space direction="vertical" size={4}>
          <Typography.Text type="secondary">Environments: {project.environmentCount}</Typography.Text>
          <Typography.Text type="secondary">
            Last updated: {formatProjectDate(project.lastUpdated)}
          </Typography.Text>
        </Space>
      </Card>

      <Card className="mt-6" title="Environments">
        {environments.length === 0 ? (
          <Empty description="No Environments yet">
            <Button type="primary" onClick={openEnvironmentModal}>
              Create an Environment
            </Button>
          </Empty>
        ) : (
          <Tabs
            activeKey={activeEnvironmentID === undefined ? undefined : String(activeEnvironmentID)}
            onChange={(key) => setActiveEnvironmentID(Number(key))}
            tabBarExtraContent={
              <Button type="primary" onClick={openEnvironmentModal}>
                New Environment
              </Button>
            }
            items={environments.map((environment) => ({
              key: String(environment.id),
              label: environment.name,
              children: (
                <Tabs
                  items={[
                    {
                      key: 'overview',
                      label: 'Overview',
                      children: (
                        <div className="flex flex-wrap items-start justify-between gap-4">
                          <div>
                            <Typography.Title className="!mb-1" level={4}>
                              {environment.name}
                            </Typography.Title>
                            <Typography.Text type="secondary">
                              Created {formatProjectDate(environment.createdAt)}
                            </Typography.Text>
                          </div>
                          <Popconfirm
                            title="Delete this Environment?"
                            description="Any Composes in it will be handled by a later ticket."
                            okText="Delete"
                            okButtonProps={{ danger: true }}
                            onConfirm={() => void deleteEnvironment(environment.id)}
                          >
                            <Button danger loading={deletingEnvironmentID === environment.id}>
                              Delete Environment
                            </Button>
                          </Popconfirm>
                        </div>
                      ),
                    },
                    {
                      key: 'variables',
                      label: 'Environment Variables',
                      children: (
                        <EnvironmentVariablesPanel projectID={projectID} environmentID={environment.id} />
                      ),
                    },
                    {
                      key: 'composes',
                      label: 'Composes',
                      children: <ComposesPanel projectID={projectID} environmentID={environment.id} />,
                    },
                  ]}
                />
              ),
            }))}
          />
        )}
      </Card>

      <Modal
        title="New Environment"
        open={environmentModalOpen}
        okText="Create Environment"
        confirmLoading={savingEnvironment}
        onCancel={closeEnvironmentModal}
        onOk={() => environmentForm.submit()}
        destroyOnClose
      >
        <Form
          form={environmentForm}
          layout="vertical"
          requiredMark={false}
          onFinish={(values) => void createEnvironment(values)}
        >
          <Form.Item
            label="Name"
            name="name"
            rules={[
              { required: true, message: 'Enter an Environment name' },
              { max: 255, message: 'Use 255 characters or fewer' },
            ]}
          >
            <Input autoFocus placeholder="e.g. staging" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}

function EnvironmentVariablesPanel({
  projectID,
  environmentID,
}: {
  projectID: number;
  environmentID: number;
}) {
  const [form] = Form.useForm<EnvironmentVariableInput>();
  const [variables, setVariables] = useState<EnvironmentVariable[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [deletingID, setDeletingID] = useState<number>();
  const [modalOpen, setModalOpen] = useState(false);
  const [editingVariable, setEditingVariable] = useState<EnvironmentVariable>();
  const [error, setError] = useState<string>();
  const requestID = useRef(0);

  const variablesPath = `/api/projects/${projectID}/environments/${environmentID}/variables`;

  const loadVariables = async () => {
    const currentRequestID = ++requestID.current;
    setLoading(true);
    setError(undefined);
    try {
      const response = await fetch(variablesPath, { credentials: 'same-origin' });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const nextVariables = (await response.json()) as EnvironmentVariable[];
      if (currentRequestID === requestID.current) {
        setVariables(nextVariables);
      }
    } catch (reason: unknown) {
      if (currentRequestID === requestID.current) {
        setError(errorMessage(reason));
      }
    } finally {
      if (currentRequestID === requestID.current) {
        setLoading(false);
      }
    }
  };

  useEffect(() => {
    void loadVariables();
    return () => {
      requestID.current += 1;
    };
  }, [variablesPath]);

  const openCreateModal = () => {
    setEditingVariable(undefined);
    form.resetFields();
    setModalOpen(true);
  };

  const openEditModal = (variable: EnvironmentVariable) => {
    setEditingVariable(variable);
    form.setFieldsValue({ key: variable.key, value: variable.value });
    setModalOpen(true);
  };

  const closeModal = () => {
    if (!saving) {
      setModalOpen(false);
      form.resetFields();
    }
  };

  const saveVariable = async (values: EnvironmentVariableInput) => {
    setSaving(true);
    setError(undefined);
    try {
      const response = await fetch(editingVariable ? `${variablesPath}/${editingVariable.id}` : variablesPath, {
        method: editingVariable ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(values),
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      setModalOpen(false);
      form.resetFields();
      await loadVariables();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };

  const deleteVariable = async (id: number) => {
    setDeletingID(id);
    setError(undefined);
    try {
      const response = await fetch(`${variablesPath}/${id}`, {
        method: 'DELETE',
        credentials: 'same-origin',
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      await loadVariables();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setDeletingID(undefined);
    }
  };

  return (
    <>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <Typography.Title className="!mb-1" level={4}>
            Environment Variables
          </Typography.Title>
          <Typography.Text type="secondary">
            Shared key-value configuration for every Compose in this Environment.
          </Typography.Text>
        </div>
        <Button type="primary" onClick={openCreateModal}>
          New Variable
        </Button>
      </div>

      {error && <Alert className="mb-4" type="error" showIcon message={error} />}

      <Spin spinning={loading}>
        {variables.length === 0 && !loading ? (
          <Empty description="No Environment Variables yet">
            <Button type="primary" onClick={openCreateModal}>
              Add your first Variable
            </Button>
          </Empty>
        ) : (
          <Table<EnvironmentVariable>
            rowKey="id"
            dataSource={variables}
            pagination={false}
            columns={[
              {
                title: 'Key',
                dataIndex: 'key',
                key: 'key',
                width: '30%',
              },
              {
                title: 'Value',
                dataIndex: 'value',
                key: 'value',
                render: (value: string) => value || <Typography.Text type="secondary">Empty</Typography.Text>,
              },
              {
                title: 'Actions',
                key: 'actions',
                width: 160,
                render: (_value: unknown, variable: EnvironmentVariable) => (
                  <Space size="small">
                    <Button type="link" onClick={() => openEditModal(variable)}>
                      Edit
                    </Button>
                    <Popconfirm
                      title="Delete this Variable?"
                      description="This action cannot be undone."
                      okText="Delete"
                      okButtonProps={{ danger: true }}
                      onConfirm={() => void deleteVariable(variable.id)}
                    >
                      <Button type="link" danger loading={deletingID === variable.id}>
                        Delete
                      </Button>
                    </Popconfirm>
                  </Space>
                ),
              },
            ]}
          />
        )}
      </Spin>

      <Modal
        title={editingVariable ? 'Edit Environment Variable' : 'New Environment Variable'}
        open={modalOpen}
        okText={editingVariable ? 'Save changes' : 'Create Variable'}
        confirmLoading={saving}
        onCancel={closeModal}
        onOk={() => form.submit()}
        destroyOnClose
      >
        <Form form={form} layout="vertical" requiredMark={false} onFinish={(values) => void saveVariable(values)}>
          <Form.Item
            label="Key"
            name="key"
            rules={[
              { required: true, message: 'Enter an Environment Variable key' },
              { max: 255, message: 'Use 255 characters or fewer' },
            ]}
          >
            <Input autoFocus placeholder="e.g. DATABASE_URL" />
          </Form.Item>
          <Form.Item
            label="Value"
            name="value"
            rules={[{ required: true, message: 'Enter an Environment Variable value' }]}
          >
            <Input placeholder="e.g. postgres://db/app" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}

const monacoEditorOptions = {
  minimap: { enabled: false },
  fontSize: 13,
  fontFamily: codeFontFamily,
  scrollBeyondLastLine: false,
} as const;

function ComposeStatusTag({ status }: { status: ComposeStatus }) {
  if (status.total === 0) {
    return <Tag>Stopped</Tag>;
  }
  return (
    <Tag color={status.running === status.total ? 'success' : 'warning'}>
      {status.running}/{status.total} running
    </Tag>
  );
}

function ComposesPanel({ projectID, environmentID }: { projectID: number; environmentID: number }) {
  const { token } = theme.useToken();
  const [form] = Form.useForm<{ name: string }>();
  const [composes, setComposes] = useState<Compose[]>([]);
  const [createYAML, setCreateYAML] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [selectedComposeID, setSelectedComposeID] = useState<number>();
  const [error, setError] = useState<string>();
  const requestID = useRef(0);

  const composesPath = `/api/projects/${projectID}/environments/${environmentID}/composes`;

  const loadComposes = async () => {
    const currentRequestID = ++requestID.current;
    setLoading(true);
    setError(undefined);
    try {
      const response = await fetch(composesPath, { credentials: 'same-origin' });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const nextComposes = (await response.json()) as Compose[];
      if (currentRequestID === requestID.current) {
        setComposes(nextComposes);
      }
    } catch (reason: unknown) {
      if (currentRequestID === requestID.current) {
        setError(errorMessage(reason));
      }
    } finally {
      if (currentRequestID === requestID.current) {
        setLoading(false);
      }
    }
  };

  useEffect(() => {
    void loadComposes();
    return () => {
      requestID.current += 1;
    };
  }, [composesPath]);

  const openCreateModal = () => {
    form.resetFields();
    setCreateYAML('');
    setModalOpen(true);
  };

  const closeModal = () => {
    if (!saving) {
      setModalOpen(false);
      form.resetFields();
      setCreateYAML('');
    }
  };

  const createCompose = async (values: { name: string }) => {
    const input: ComposeCreateInput = { name: values.name, yamlContent: createYAML };
    setSaving(true);
    setError(undefined);
    try {
      const response = await fetch(composesPath, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(input),
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const created = (await response.json()) as Compose;
      setModalOpen(false);
      form.resetFields();
      setCreateYAML('');
      await loadComposes();
      setSelectedComposeID(created.id);
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };

  if (selectedComposeID !== undefined) {
    return (
      <ComposeDetailPanel
        projectID={projectID}
        environmentID={environmentID}
        composeID={selectedComposeID}
        onBack={() => {
          setSelectedComposeID(undefined);
          void loadComposes();
        }}
      />
    );
  }

  return (
    <>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <Typography.Title className="!mb-1" level={4}>
            Composes
          </Typography.Title>
          <Typography.Text type="secondary">
            Docker Compose stacks deployed within this Environment.
          </Typography.Text>
        </div>
        <Button type="primary" onClick={openCreateModal}>
          New Compose
        </Button>
      </div>

      {error && <Alert className="mb-4" type="error" showIcon message={error} />}

      <Spin spinning={loading}>
        {composes.length === 0 && !loading ? (
          <Empty description="No Composes yet">
            <Button type="primary" onClick={openCreateModal}>
              Create your first Compose
            </Button>
          </Empty>
        ) : (
          <Table<Compose>
            rowKey="id"
            dataSource={composes}
            pagination={false}
            onRow={(compose) => ({
              onClick: () => setSelectedComposeID(compose.id),
              className: 'cursor-pointer',
            })}
            columns={[
              {
                title: 'Name',
                dataIndex: 'name',
                key: 'name',
              },
              {
                title: 'Status',
                key: 'status',
                width: 160,
                render: (_value: unknown, compose: Compose) => <ComposeStatusTag status={compose.status} />,
              },
              {
                title: 'Last updated',
                key: 'updatedAt',
                width: 200,
                render: (_value: unknown, compose: Compose) => formatProjectDate(compose.updatedAt),
              },
            ]}
          />
        )}
      </Spin>

      <Modal
        title="New Compose"
        open={modalOpen}
        okText="Create Compose"
        confirmLoading={saving}
        onCancel={closeModal}
        onOk={() => form.submit()}
        width={720}
        destroyOnClose
      >
        <Form form={form} layout="vertical" requiredMark={false} onFinish={(values) => void createCompose(values)}>
          <Form.Item
            label="Name"
            name="name"
            rules={[
              { required: true, message: 'Enter a Compose name' },
              { max: 255, message: 'Use 255 characters or fewer' },
            ]}
          >
            <Input autoFocus placeholder="e.g. web" />
          </Form.Item>
        </Form>
        <Typography.Text type="secondary">docker-compose.yml</Typography.Text>
        <div
          className="mt-2 overflow-hidden"
          style={{ border: `1px solid ${token.colorBorderSecondary}`, borderRadius: token.borderRadius }}
        >
          <Editor
            height="320px"
            language="yaml"
            theme="vs"
            value={createYAML}
            onChange={(value) => setCreateYAML(value ?? '')}
            options={monacoEditorOptions}
          />
        </div>
      </Modal>
    </>
  );
}

function ComposeDetailPanel({
  projectID,
  environmentID,
  composeID,
  onBack,
}: {
  projectID: number;
  environmentID: number;
  composeID: number;
  onBack: () => void;
}) {
  const { token } = theme.useToken();
  const [compose, setCompose] = useState<Compose>();
  const [yamlContent, setYamlContent] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [deploying, setDeploying] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState<string>();
  const requestID = useRef(0);

  const composePath = `/api/projects/${projectID}/environments/${environmentID}/composes/${composeID}`;

  const loadCompose = async () => {
    const currentRequestID = ++requestID.current;
    setLoading(true);
    setError(undefined);
    try {
      const response = await fetch(composePath, { credentials: 'same-origin' });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const loaded = (await response.json()) as Compose;
      if (currentRequestID === requestID.current) {
        setCompose(loaded);
        setYamlContent(loaded.yamlContent);
      }
    } catch (reason: unknown) {
      if (currentRequestID === requestID.current) {
        setError(errorMessage(reason));
      }
    } finally {
      if (currentRequestID === requestID.current) {
        setLoading(false);
      }
    }
  };

  useEffect(() => {
    void loadCompose();
    return () => {
      requestID.current += 1;
    };
  }, [composePath]);

  const saveYAML = async () => {
    setSaving(true);
    setError(undefined);
    try {
      const response = await fetch(composePath, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ yamlContent }),
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const updated = (await response.json()) as Compose;
      setCompose(updated);
      setYamlContent(updated.yamlContent);
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };

  // "Save & Deploy" per the spec's Out of Scope note: v1 has no auto-deploy,
  // so the manual deploy action also persists any pending YAML edits first.
  const saveAndDeploy = async () => {
    setDeploying(true);
    setError(undefined);
    try {
      const saveResponse = await fetch(composePath, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ yamlContent }),
      });
      if (!saveResponse.ok) {
        throw new Error(await responseError(saveResponse));
      }
      const deployResponse = await fetch(`${composePath}/deploy`, {
        method: 'POST',
        credentials: 'same-origin',
      });
      if (!deployResponse.ok) {
        throw new Error(await responseError(deployResponse));
      }
      const deployed = (await deployResponse.json()) as Compose;
      setCompose(deployed);
      setYamlContent(deployed.yamlContent);
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setDeploying(false);
    }
  };

  const stop = async () => {
    setStopping(true);
    setError(undefined);
    try {
      const response = await fetch(`${composePath}/stop`, {
        method: 'POST',
        credentials: 'same-origin',
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const stopped = (await response.json()) as Compose;
      setCompose(stopped);
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setStopping(false);
    }
  };

  if (loading && !compose) {
    return (
      <div className="flex justify-center py-12">
        <Spin size="large" />
      </div>
    );
  }

  if (!compose) {
    return (
      <>
        <Button className="!mb-4" onClick={onBack}>
          Back to Composes
        </Button>
        <Alert type="error" showIcon message={error ?? 'Compose not found'} />
      </>
    );
  }

  const dirty = yamlContent !== compose.yamlContent;

  return (
    <>
      <Button className="!mb-4" onClick={onBack}>
        Back to Composes
      </Button>
      {error && <Alert className="mb-4" type="error" showIcon message={error} />}
      <div className="mb-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <Typography.Title className="!mb-1" level={4}>
            {compose.name}
          </Typography.Title>
          <Typography.Text type="secondary">Last updated {formatProjectDate(compose.updatedAt)}</Typography.Text>
        </div>
        <Space>
          <ComposeStatusTag status={compose.status} />
          <Button loading={loading} onClick={() => void loadCompose()}>
            Refresh
          </Button>
        </Space>
      </div>
      <Tabs
        items={[
          {
            key: 'general',
            label: 'General',
            children: (
              <div>
                <div className="mb-3 flex justify-end gap-2">
                  <Button loading={saving} disabled={!dirty} onClick={() => void saveYAML()}>
                    Save changes
                  </Button>
                  <Button type="primary" loading={deploying} onClick={() => void saveAndDeploy()}>
                    Save & Deploy
                  </Button>
                  <Button danger loading={stopping} disabled={compose.status.total === 0} onClick={() => void stop()}>
                    Stop
                  </Button>
                </div>
                <div
                  className="overflow-hidden"
                  style={{ border: `1px solid ${token.colorBorderSecondary}`, borderRadius: token.borderRadius }}
                >
                  <Editor
                    height="480px"
                    language="yaml"
                    theme="vs"
                    value={yamlContent}
                    onChange={(value) => setYamlContent(value ?? '')}
                    options={monacoEditorOptions}
                  />
                </div>
              </div>
            ),
          },
        ]}
      />
    </>
  );
}

function selectEnvironmentID(environments: Environment[], preferredID?: number): number | undefined {
  if (preferredID !== undefined && environments.some((environment) => environment.id === preferredID)) {
    return preferredID;
  }
  return environments.find((environment) => environment.name.toLowerCase() === 'production')?.id ?? environments[0]?.id;
}

function formatProjectDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return 'Unknown';
  }
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <ConfigProvider theme={{ algorithm: theme.defaultAlgorithm }}>
        <App />
      </ConfigProvider>
    </StyleProvider>
  </StrictMode>,
);
