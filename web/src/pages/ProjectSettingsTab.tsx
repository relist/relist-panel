import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { App, Button, Card, Flex, Form, Input, Table, Tag, Typography, theme } from 'antd';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';

import { paths } from '../app/paths';
import { DangerModal } from '../components/DangerModal';
import { TeardownDetails } from '../components/TeardownDetails';
import { RelativeTime } from '../components/primitives';
import type { ProjectInput } from '../data/api';
import { usePanel } from '../data/store';
import type { Environment, Project } from '../data/types';
import { environmentDeleteFacts, projectDeleteFacts } from './project-view';

const { Text } = Typography;

/**
 * The Project screen's Settings tab (GUIDELINES §4.4): the Project details
 * form, the Environment list with delete, and the danger zone for deleting the
 * Project — the jobs the temporary `Manage` dropdown held, moved to their real
 * home. Parent deletion tears down each Compose's containers, networks,
 * volumes and files before removing metadata; failures preserve the records
 * without undoing child teardowns that already completed.
 */
export function ProjectSettingsTab({
  activeEnvironmentId,
  onNewEnvironment,
  onError,
  project,
}: {
  /** The Environment the switcher shows, so deleting it can fall back cleanly. */
  activeEnvironmentId?: number;
  onNewEnvironment: () => void;
  onError: (reason: unknown) => void;
  project: Project;
}) {
  const { deleteEnvironment, deleteProject, isPending, updateProject } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const navigate = useNavigate();
  const [projectForm] = Form.useForm<ProjectInput>();
  const [deletingEnvironment, setDeletingEnvironment] = useState<Environment>();
  const [deleteProjectOpen, setDeleteProjectOpen] = useState(false);

  // Prefill from the tree, keyed by the fields rather than the Project object
  // so an unrelated tree patch (a deploy, a variable save) does not clobber a
  // half-edited form.
  useEffect(() => {
    projectForm.setFieldsValue({ description: project.description, name: project.name });
  }, [project.description, projectForm, project.id, project.name]);

  const saveProject = async (values: ProjectInput) => {
    try {
      await updateProject(project.id, values);
      message.success('Saved Project details');
    } catch (reason: unknown) {
      onError(reason);
    }
  };

  const removeEnvironment = async () => {
    if (!deletingEnvironment) return;
    const name = deletingEnvironment.name;
    const wasActive = deletingEnvironment.id === activeEnvironmentId;
    try {
      await deleteEnvironment(project.id, deletingEnvironment.id);
      setDeletingEnvironment(undefined);
      message.success(`Deleted Environment ${name}`);
      if (wasActive) {
        navigate(paths.project(project.id));
      }
    } catch (reason: unknown) {
      onError(reason);
    }
  };

  const removeProject = async () => {
    const name = project.name;
    try {
      await deleteProject(project.id);
      setDeleteProjectOpen(false);
      message.success(`Deleted ${name}`);
      navigate(paths.projects);
    } catch (reason: unknown) {
      onError(reason);
    }
  };

  const environmentFacts = deletingEnvironment ? environmentDeleteFacts(deletingEnvironment) : undefined;
  const projectFacts = projectDeleteFacts(project);

  return (
    <>
      <Flex gap={16} vertical>
        <Card title="Project details" variant="outlined">
          <Form
            form={projectForm}
            layout="vertical"
            onFinish={(values) => void saveProject(values)}
            requiredMark={false}
          >
            <Form.Item
              extra="Renaming does not move already-deployed Composes — deploy identity is frozen at Compose creation (ADR-0011)."
              label="Name"
              name="name"
              rules={[
                { message: 'Enter a Project name', required: true },
                { max: 255, message: 'Use 255 characters or fewer' },
              ]}
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

        <Card
          extra={
            <Button icon={<PlusOutlined />} onClick={onNewEnvironment} size="small">
              New Environment
            </Button>
          }
          title="Environments"
          variant="outlined"
        >
          <Table<Environment>
            columns={[
              { dataIndex: 'name', key: 'name', title: 'Environment' },
              {
                key: 'composes',
                render: (_value, environment) => <Tag>{environment.composes.length} Composes</Tag>,
                title: '',
                width: 160,
              },
              {
                dataIndex: 'createdAt',
                key: 'created',
                render: (value: string) => (
                  <Text type="secondary">
                    Created <RelativeTime value={value} />
                  </Text>
                ),
                title: 'Created',
                width: 200,
              },
              {
                key: 'actions',
                render: (_value, environment) => (
                  <Button
                    aria-label={`Delete ${environment.name}`}
                    danger
                    icon={<DeleteOutlined />}
                    loading={isPending(`delete-environment:${environment.id}`)}
                    onClick={() => setDeletingEnvironment(environment)}
                    size="small"
                    type="text"
                  />
                ),
                title: '',
                width: 70,
              },
            ]}
            dataSource={project.environments}
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
                Removes this Project, its {projectFacts.environments}{' '}
                {projectFacts.environments === 1 ? 'Environment' : 'Environments'} and {projectFacts.composes}{' '}
                {projectFacts.composes === 1 ? 'Compose' : 'Composes'}. This first stops and removes their containers,
                networks, volumes and deployment files. Volume data is permanently deleted.
              </Text>
            </div>
            <Button danger icon={<DeleteOutlined />} onClick={() => setDeleteProjectOpen(true)}>
              Delete Project
            </Button>
          </Flex>
        </Card>
      </Flex>

      <DangerModal
        confirmWord={deletingEnvironment?.name ?? ''}
        consequences={
          <div>
            {deletingEnvironment && environmentFacts && (
              <>
                This deletes {deletingEnvironment.name} and its {environmentFacts.composes}{' '}
                {environmentFacts.composes === 1 ? 'Compose' : 'Composes'} from the panel, after stopping and removing
                their containers, networks, volumes and deployment files. Volume data is permanently deleted.
                <TeardownDetails directories={environmentFacts.directories} />
              </>
            )}
          </div>
        }
        description="Deleting an Environment cannot be undone."
        loading={isPending(`delete-environment:${deletingEnvironment?.id ?? 0}`)}
        okText="Delete Environment"
        onCancel={() => setDeletingEnvironment(undefined)}
        onConfirm={() => void removeEnvironment()}
        open={Boolean(deletingEnvironment)}
        title="Delete this Environment?"
      />

      <DangerModal
        confirmWord={project.name}
        consequences={
          <div>
            This removes {project.name}, its {projectFacts.environments}{' '}
            {projectFacts.environments === 1 ? 'Environment' : 'Environments'} and {projectFacts.composes}{' '}
            {projectFacts.composes === 1 ? 'Compose' : 'Composes'}, after stopping and removing their containers,
            networks, volumes and deployment files. Volume data is permanently deleted.
            <TeardownDetails directories={projectFacts.directories} />
          </div>
        }
        description="Deleting a Project cannot be undone."
        loading={isPending(`delete-project:${project.id}`)}
        okText="Delete Project"
        onCancel={() => setDeleteProjectOpen(false)}
        onConfirm={() => void removeProject()}
        open={deleteProjectOpen}
        title="Delete this Project?"
      />
    </>
  );
}
