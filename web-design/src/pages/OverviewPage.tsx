import {
  AppstoreOutlined,
  ArrowRightOutlined,
  CloudServerOutlined,
  FolderOutlined,
  PlusOutlined,
  ReloadOutlined,
  RocketOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import { Alert, Button, Card, Col, Empty, Flex, List, Row, Table, Tag, Timeline, Tooltip, Typography, theme } from 'antd';
import { navigate, paths } from '../app/router';
import { useUiState } from '../app/uiState';
import { ComposeStatusInline, ComposeStatusTag, PageHeader, RelativeTime, StatCard } from '../components/primitives';
import { LiveHandlesBanner } from '../components/LiveHandlesBanner';
import { usePanel } from '../data/store';
import type { Compose } from '../data/types';
import { composeState, summarize } from '../data/types';

const { Text } = Typography;

type FlatCompose = {
  compose: Compose;
  environmentId: number;
  environmentName: string;
  projectId: number;
  projectName: string;
};

/**
 * Host-level landing page. The old design dropped straight into a Projects
 * list, which answers "what exists?" but not "is anything broken right now?" —
 * the question an operator actually opens the panel with.
 */
export function OverviewPage() {
  const { activity, projects, refreshStatus } = usePanel();
  const { token } = theme.useToken();
  const [refreshing, setRefreshing] = useUiState('overview.refreshing', false);

  const flat: FlatCompose[] = projects.flatMap((project) =>
    project.environments.flatMap((environment) =>
      environment.composes.map((compose) => ({
        compose,
        environmentId: environment.id,
        environmentName: environment.name,
        projectId: project.id,
        projectName: project.name,
      })),
    ),
  );

  const rollup = summarize(flat.map((entry) => entry.compose));
  const attention = flat.filter(({ compose }) => {
    const state = composeState(compose.status, compose.lastDeployedAt);
    return state === 'partial' || (state === 'stopped' && compose.status.total > 0) || compose.services.some((service) => service.health === 'unhealthy');
  });

  const refreshAll = async () => {
    setRefreshing(true);
    try {
      await Promise.all(flat.map(({ compose }) => refreshStatus(compose.id)));
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <>
      <PageHeader
        description="Deployments running on this host, in one glance."
        extra={
          <>
            <Button icon={<ReloadOutlined />} loading={refreshing} onClick={() => void refreshAll()}>
              Refresh statuses
            </Button>
            <Button icon={<PlusOutlined />} onClick={() => navigate(paths.projects)} type="primary">
              New Project
            </Button>
          </>
        }
        title="Overview"
      />

      {attention.length > 0 && (
        <Alert
          action={
            <Button
              onClick={() => navigate(paths.compose(attention[0].projectId, attention[0].environmentId, attention[0].compose.id))}
              size="small"
              type="link"
            >
              Review
            </Button>
          }
          message={`${attention.length} ${attention.length === 1 ? 'Compose needs' : 'Composes need'} attention`}
          description="Partly running, or stopped after a successful deploy. They are listed below with the service that dropped out."
          showIcon
          style={{ marginBottom: 24 }}
          type="warning"
        />
      )}

      <LiveHandlesBanner />

      <Row gutter={[16, 16]}>
        <Col lg={6} sm={12} xs={24}>
          <StatCard hint={`${projects.length} Projects on this host`} icon={<FolderOutlined />} title="Projects" value={projects.length} />
        </Col>
        <Col lg={6} sm={12} xs={24}>
          <StatCard
            hint={`${projects.reduce((sum, project) => sum + project.environments.length, 0)} deployment targets`}
            icon={<AppstoreOutlined />}
            title="Environments"
            value={projects.reduce((sum, project) => sum + project.environments.length, 0)}
          />
        </Col>
        <Col lg={6} sm={12} xs={24}>
          <StatCard hint={`${flat.filter(({ compose }) => compose.status.total === 0).length} never deployed`} icon={<RocketOutlined />} title="Composes" value={rollup.composes} />
        </Col>
        <Col lg={6} sm={12} xs={24}>
          <StatCard
            hint={
              <span>
                {rollup.running} of {rollup.total} containers up
              </span>
            }
            icon={<CloudServerOutlined />}
            title="Containers running"
            value={
              <span>
                {rollup.running}
                <Text style={{ fontSize: 16 }} type="secondary">
                  {' '}
                  / {rollup.total}
                </Text>
              </span>
            }
          />
        </Col>
      </Row>

      <Row align="top" gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col lg={15} xs={24}>
          <Card
            extra={
              attention.length > 0 ? (
                <Tag color="warning" variant="filled">
                  {attention.length}
                </Tag>
              ) : (
                <Tag color="success" variant="filled">
                  All clear
                </Tag>
              )
            }
            styles={{ body: { padding: attention.length === 0 ? 0 : undefined } }}
            title={
              <Flex align="center" gap={8}>
                <WarningOutlined style={{ color: attention.length > 0 ? token.colorWarning : token.colorTextTertiary }} />
                Needs attention
              </Flex>
            }
            variant="outlined"
          >
            {attention.length === 0 ? (
              <Empty description="Every deployed Compose is fully up." image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ) : (
              <List
                dataSource={attention}
                renderItem={({ compose, environmentName, projectName, projectId, environmentId }) => {
                  const state = composeState(compose.status, compose.lastDeployedAt);
                  const unhealthy = compose.services.filter((service) => service.health === 'unhealthy');
                  const down = compose.services.filter((service) => service.state === 'exited');
                  return (
                    <List.Item
                      actions={[
                        <Button key="open" onClick={() => navigate(paths.compose(projectId, environmentId, compose.id))} size="small" type="link">
                          Open <ArrowRightOutlined />
                        </Button>,
                      ]}
                    >
                      <List.Item.Meta
                        description={
                          <Flex align="center" gap={8} wrap>
                            <ComposeStatusTag lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
                            {down.length > 0 && <Text type="secondary">{down.map((service) => service.name).join(', ')} exited</Text>}
                            {unhealthy.length > 0 && <Text type="secondary">{unhealthy.map((service) => service.name).join(', ')} unhealthy</Text>}
                            {state === 'stopped' && <Text type="secondary">Stopped {compose.lastDeployedAt ? 'after a successful deploy' : 'before ever going live'}</Text>}
                          </Flex>
                        }
                        title={
                          <Flex align="center" gap={6}>
                            <Text strong>{compose.name}</Text>
                            <Text type="secondary">
                              {projectName} / {environmentName}
                            </Text>
                          </Flex>
                        }
                      />
                    </List.Item>
                  );
                }}
                size="small"
              />
            )}
          </Card>
        </Col>
        <Col lg={9} xs={24}>
          <Card title="Recent activity" variant="outlined">
            <Timeline
              items={activity.slice(0, 6).map((entry) => ({
                color: entry.ok ? (entry.action === 'compose.stop' ? 'gray' : 'green') : 'red',
                children: (
                  <div>
                    <Text>{entry.detail}</Text>
                    <br />
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      <RelativeTime value={entry.at} /> · {entry.actor}
                    </Text>
                  </div>
                ),
              }))}
            />
          </Card>
        </Col>
      </Row>

      <Card
        extra={<Text type="secondary">Every Compose across every Project</Text>}
        style={{ marginTop: 16 }}
        title="All Composes"
        variant="outlined"
      >
        <Table<FlatCompose>
          columns={[
            {
              dataIndex: 'compose',
              key: 'name',
              render: (_value, row) => (
                <Flex align="center" gap={8}>
                  <RocketOutlined style={{ color: token.colorTextTertiary }} />
                  <Text strong>{row.compose.name}</Text>
                  <ComposeStatusTag lastDeployedAt={row.compose.lastDeployedAt} status={row.compose.status} />
                </Flex>
              ),
              title: 'Compose',
            },
            {
              key: 'where',
              render: (_value, row) => (
                <Flex gap={6}>
                  <Text type="secondary">{row.projectName}</Text>
                  <Text type="secondary">/</Text>
                  <Text>{row.environmentName}</Text>
                </Flex>
              ),
              title: 'Project / Environment',
              width: 240,
            },
            {
              key: 'status',
              render: (_value, row) => <ComposeStatusInline lastDeployedAt={row.compose.lastDeployedAt} status={row.compose.status} />,
              title: 'Containers',
              width: 200,
            },
            {
              key: 'updated',
              render: (_value, row) => (
                <Tooltip title={<RelativeTime value={row.compose.updatedAt} />}>
                  <Text type="secondary">
                    <RelativeTime value={row.compose.updatedAt} />
                  </Text>
                </Tooltip>
              ),
              title: 'Last edited',
              width: 160,
            },
          ]}
          dataSource={flat}
          onRow={(row) => ({
            className: 'rp-clickable-row',
            onClick: () => navigate(paths.compose(row.projectId, row.environmentId, row.compose.id)),
          })}
          pagination={false}
          rowKey={(row) => row.compose.id}
          size="middle"
        />
      </Card>
    </>
  );
}
