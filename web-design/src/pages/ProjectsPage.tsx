import {
  AppstoreOutlined,
  DeleteOutlined,
  EditOutlined,
  FolderOpenOutlined,
  MoreOutlined,
  PlusOutlined,
  RocketOutlined,
  SearchOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import {
  App,
  Button,
  Card,
  Col,
  Dropdown,
  Flex,
  Form,
  Input,
  Modal,
  Row,
  Segmented,
  Select,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { MenuProps } from 'antd';
import { useMemo, useState } from 'react';
import { navigate, paths } from '../app/router';
import { DangerModal } from '../components/DangerModal';
import { ComposeStatusTag, EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { usePanel } from '../data/store';
import type { Project } from '../data/types';
import { projectComposes, summarize } from '../data/types';

const { Text, Paragraph } = Typography;

type SortKey = 'updated' | 'name' | 'size';

/**
 * Projects: the same information as before, but the card is no longer the only
 * affordance — operators who run many Projects get a table with sorting, and
 * both views keep the status rollup visible so "which app is broken" never
 * requires drilling in.
 */
export function ProjectsPage() {
  const { createProject, deleteProject, projects, updateProject, isPending } = usePanel();
  const { message } = App.useApp();
  const [form] = Form.useForm<{ name: string; description: string }>();
  const [query, setQuery] = useState('');
  const [sort, setSort] = useState<SortKey>('updated');
  const [view, setView] = useState<'cards' | 'table'>('cards');
  const [editing, setEditing] = useState<Project>();
  const [modalOpen, setModalOpen] = useState(false);
  const [deleting, setDeleting] = useState<Project>();

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const filtered = needle
      ? projects.filter(
          (project) =>
            project.name.toLowerCase().includes(needle) || project.description.toLowerCase().includes(needle),
        )
      : projects;
    return [...filtered].sort((a, b) => {
      if (sort === 'name') return a.name.localeCompare(b.name);
      if (sort === 'size') return b.environments.length - a.environments.length;
      return new Date(b.lastUpdated).getTime() - new Date(a.lastUpdated).getTime();
    });
  }, [projects, query, sort]);

  const openCreate = () => {
    setEditing(undefined);
    form.resetFields();
    setModalOpen(true);
  };

  const openEdit = (project: Project) => {
    setEditing(project);
    form.setFieldsValue({ description: project.description, name: project.name });
    setModalOpen(true);
  };

  const submit = async (values: { name: string; description: string }) => {
    try {
      if (editing) {
        await updateProject(editing.id, values);
        message.success(`Saved ${values.name}`);
      } else {
        const created = await createProject(values);
        message.success(`Created ${created.name}`);
      }
      setModalOpen(false);
    } catch {
      message.error('Could not save the Project');
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    const name = deleting.name;
    await deleteProject(deleting.id);
    setDeleting(undefined);
    message.success(`Deleted ${name}`);
  };

  const rowMenu = (project: Project): MenuProps['items'] => [
    { icon: <FolderOpenOutlined />, key: 'open', label: 'Open', onClick: () => navigate(paths.project(project.id)) },
    { icon: <EditOutlined />, key: 'edit', label: 'Edit details', onClick: () => openEdit(project) },
    { type: 'divider' },
    { danger: true, icon: <DeleteOutlined />, key: 'delete', label: 'Delete', onClick: () => setDeleting(project) },
  ];

  return (
    <>
      <PageHeader
        description="A Project groups the Environments of one application — production, staging, preview."
        extra={
          <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
            New Project
          </Button>
        }
        title="Projects"
      />

      <Flex align="center" gap={8} justify="space-between" style={{ marginBottom: 16 }} wrap>
        <Input
          allowClear
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Filter by name or description"
          prefix={<SearchOutlined />}
          style={{ maxWidth: 320 }}
          value={query}
        />
        <Flex align="center" gap={8}>
          <Select
            onChange={(value) => setSort(value)}
            options={[
              { label: 'Recently updated', value: 'updated' },
              { label: 'Name (A–Z)', value: 'name' },
              { label: 'Most Environments', value: 'size' },
            ]}
            style={{ width: 190 }}
            value={sort}
          />
          <Segmented
            onChange={(value) => setView(value as 'cards' | 'table')}
            options={[
              { icon: <AppstoreOutlined />, label: 'Cards', value: 'cards' },
              { icon: <UnorderedListOutlined />, label: 'Table', value: 'table' },
            ]}
            value={view}
          />
        </Flex>
      </Flex>

      {visible.length === 0 ? (
        <Card variant="outlined">
          <EmptyState
            action={
              <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
                Create a Project
              </Button>
            }
            description={
              query
                ? `Nothing matches “${query}”. Try a different search.`
                : 'A Project is the top level of the panel: one application, with its own Environments and Composes.'
            }
            title={query ? 'No Projects match' : 'No Projects yet'}
          />
        </Card>
      ) : view === 'cards' ? (
        <Row gutter={[16, 16]}>
          {visible.map((project) => {
            const composes = projectComposes(project);
            const rollup = summarize(composes);
            return (
              <Col key={project.id} lg={8} md={12} xs={24}>
                <Card
                  actions={[
                    <Button icon={<FolderOpenOutlined />} key="open" onClick={() => navigate(paths.project(project.id))} type="link">
                      Open
                    </Button>,
                    <Dropdown key="more" menu={{ items: rowMenu(project) }} placement="bottomRight" trigger={['click']}>
                      <Button icon={<MoreOutlined />} type="text" />
                    </Dropdown>,
                  ]}
                  loading={isPending(`delete-project:${project.id}`)}
                  styles={{ body: { minHeight: 148 } }}
                  title={
                    <Flex align="center" gap={8} justify="space-between" style={{ minWidth: 0 }}>
                      <Text strong ellipsis style={{ maxWidth: 150 }}>
                        {project.name}
                      </Text>
                      <ComposeStatusTag
                        lastDeployedAt={composes.find((compose) => compose.lastDeployedAt)?.lastDeployedAt}
                        status={{ running: rollup.running, total: rollup.total }}
                      />
                    </Flex>
                  }
                  variant="outlined"
                >
                  <Paragraph ellipsis={{ rows: 2 }} style={{ marginBottom: 12 }} type={project.description ? undefined : 'secondary'}>
                    {project.description || 'No description yet.'}
                  </Paragraph>
                  <Flex gap={16} wrap>
                    <Text type="secondary">
                      <AppstoreOutlined /> {project.environments.length} env{project.environments.length === 1 ? '' : 's'}
                    </Text>
                    <Text type="secondary">
                      <RocketOutlined /> {composes.length} compose{composes.length === 1 ? '' : 's'}
                    </Text>
                  </Flex>
                  <Text type="secondary" style={{ display: 'block', fontSize: 12, marginTop: 8 }}>
                    Updated <RelativeTime value={project.lastUpdated} />
                  </Text>
                </Card>
              </Col>
            );
          })}
        </Row>
      ) : (
        <Card styles={{ body: { padding: 0 } }} variant="outlined">
          <Table<Project>
            columns={[
              {
                dataIndex: 'name',
                key: 'name',
                render: (_value, project) => (
                  <Flex align="center" gap={8}>
                    <Text strong>{project.name}</Text>
                  </Flex>
                ),
                sorter: (a, b) => a.name.localeCompare(b.name),
                title: 'Project',
              },
              {
                dataIndex: 'description',
                key: 'description',
                render: (description: string) => (
                  <Text ellipsis style={{ maxWidth: 360 }} type={description ? undefined : 'secondary'}>
                    {description || '—'}
                  </Text>
                ),
                title: 'Description',
              },
              {
                key: 'environments',
                render: (_value, project) => <Tag>{project.environments.length}</Tag>,
                title: 'Envs',
                width: 90,
              },
              {
                key: 'composes',
                render: (_value, project) => <Tag>{projectComposes(project).length}</Tag>,
                title: 'Composes',
                width: 110,
              },
              {
                key: 'status',
                render: (_value, project) => {
                  const composes = projectComposes(project);
                  const rollup = summarize(composes);
                  return (
                    <ComposeStatusTag
                      lastDeployedAt={composes.find((compose) => compose.lastDeployedAt)?.lastDeployedAt}
                      status={{ running: rollup.running, total: rollup.total }}
                    />
                  );
                },
                title: 'Containers',
                width: 180,
              },
              {
                key: 'updated',
                render: (_value, project) => (
                  <Text type="secondary">
                    <RelativeTime value={project.lastUpdated} />
                  </Text>
                ),
                sorter: (a, b) => new Date(a.lastUpdated).getTime() - new Date(b.lastUpdated).getTime(),
                title: 'Updated',
                width: 160,
              },
              {
                key: 'actions',
                render: (_value, project) => (
                  <Dropdown menu={{ items: rowMenu(project) }} placement="bottomRight" trigger={['click']}>
                    <Button icon={<MoreOutlined />} type="text" />
                  </Dropdown>
                ),
                width: 70,
              },
            ]}
            dataSource={visible}
            onRow={(project) => ({
              className: 'rp-clickable-row',
              onClick: () => navigate(paths.project(project.id)),
            })}
            pagination={false}
            rowKey="id"
          />
        </Card>
      )}

      <Modal
        confirmLoading={isPending(editing ? `save-project:${editing.id}` : 'create-project')}
        destroyOnHidden
        okText={editing ? 'Save changes' : 'Create Project'}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        open={modalOpen}
        title={editing ? `Edit ${editing.name}` : 'New Project'}
      >
        <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} requiredMark={false}>
          <Form.Item
            extra="Used in deploy identities — renaming a Project does not move already-deployed stacks."
            label="Name"
            name="name"
            rules={[
              { message: 'Enter a Project name', required: true },
              { max: 255, message: 'Use 255 characters or fewer' },
            ]}
          >
            <Input autoFocus placeholder="e.g. my-saas" />
          </Form.Item>
          <Form.Item label="Description" name="description">
            <Input.TextArea placeholder="What runs in this Project?" rows={3} />
          </Form.Item>
        </Form>
      </Modal>

      <DangerModal
        confirmWord={deleting?.name ?? ''}
        consequences={
          <Text type="secondary">
            {deleting && (
              <>
                This removes {deleting.environments.length} Environment(s),{' '}
                {projectComposes(deleting).length} Compose(s), their containers, networks and volumes, and their files under{' '}
                <Text code>dataDir/{deleting.name}/</Text>.
              </>
            )}
          </Text>
        }
        description="Deleting a Project stops and removes every Compose inside it."
        loading={isPending(`delete-project:${deleting?.id ?? 0}`)}
        okText="Delete Project"
        onCancel={() => setDeleting(undefined)}
        onConfirm={() => void confirmDelete()}
        open={Boolean(deleting)}
        title="Delete this Project?"
      />
    </>
  );
}
