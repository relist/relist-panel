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
import { Alert, App, Button, Card, Col, Empty, Flex, List, Row, Table, Tag, Tooltip, Typography, theme } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router';

import { paths } from '../app/paths';
import { ComposeStatusInline, ComposeStatusTag, EmptyState, PageHeader, RelativeTime, StatCard } from '../components/primitives';
import { usePanel } from '../data/store';
import { attentionRows, composeComparators, degradationNotes, flattenComposes, overviewStats } from './overview-view';
import type { FlatCompose } from './overview-view';

const { Text } = Typography;

/**
 * Host-level landing page (GUIDELINES §4.1): answer "is anything broken?" in
 * one screen. The four stat cards, the needs-attention list, and a flat
 * all-Composes table built client-side from the Project tree — the same loop
 * the mock runs, since no flat endpoint exists. The prototype's Activity card
 * is deliberately absent (§11: the panel records no events).
 */
export function OverviewPage() {
  const { projects, refreshStatus, services, loadServices } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const navigate = useNavigate();
  const [refreshing, setRefreshing] = useState(false);

  const flat = useMemo(() => flattenComposes(projects), [projects]);
  const stats = overviewStats(projects);
  const attention = useMemo(() => attentionRows(flat, services), [flat, services]);

  // Per-Service health feeds the attention rule and the degrade notes; the
  // rows come from the Services endpoint (the Compose carries no services
  // array), so they are loaded alongside the tree and refetched whenever it
  // changes. A failed read leaves the counts in charge.
  useEffect(() => {
    for (const { compose } of flat) {
      loadServices(compose.id).catch(() => {});
    }
  }, [flat, loadServices]);

  // Loading belongs to the control that started it (§8): the button carries
  // the spinner, the table keeps showing what it read before.
  const refreshAll = async () => {
    setRefreshing(true);
    try {
      await Promise.all(flat.map(({ compose }) => refreshStatus(compose.id)));
    } catch (reason: unknown) {
      message.error(reason instanceof Error ? reason.message : 'Could not refresh statuses');
    } finally {
      setRefreshing(false);
    }
  };

  const openCompose = (row: FlatCompose) => navigate(paths.compose(row.projectId, row.environmentId, row.compose.id));

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

      {projects.length === 0 ? (
        <Card variant="outlined">
          <EmptyState
            action={
              <Button icon={<PlusOutlined />} onClick={() => navigate(paths.projects)} type="primary">
                Create the first Project
              </Button>
            }
            description="A Project groups the Environments of one application; each Environment holds its Composes and their Variables."
            title="No Projects yet"
          />
        </Card>
      ) : (
        <>
          {attention.length > 0 && (
            <Alert
              action={
                <Button onClick={() => openCompose(attention[0])} size="small" type="link">
                  Review
                </Button>
              }
              description="Partly running, or stopped after a successful deploy. They are listed below with the service that dropped out."
              message={`${attention.length} ${attention.length === 1 ? 'Compose needs' : 'Composes need'} attention`}
              showIcon
              style={{ marginBottom: 16 }}
              type="warning"
            />
          )}

          <Row gutter={[16, 16]}>
            <Col lg={6} sm={12} xs={24}>
              <StatCard hint={`${stats.projects} Projects on this host`} icon={<FolderOutlined />} title="Projects" value={stats.projects} />
            </Col>
            <Col lg={6} sm={12} xs={24}>
              <StatCard hint={`${stats.environments} deployment targets`} icon={<AppstoreOutlined />} title="Environments" value={stats.environments} />
            </Col>
            <Col lg={6} sm={12} xs={24}>
              <StatCard hint={`${stats.neverDeployed} never deployed`} icon={<RocketOutlined />} title="Composes" value={stats.composes} />
            </Col>
            <Col lg={6} sm={12} xs={24}>
              <StatCard
                hint={
                  <span>
                    {stats.running} of {stats.containers} containers up
                  </span>
                }
                icon={<CloudServerOutlined />}
                title="Containers running"
                value={
                  <span>
                    {stats.running}
                    <Text style={{ fontSize: 16 }} type="secondary">
                      {' '}
                      / {stats.containers}
                    </Text>
                  </span>
                }
              />
            </Col>
          </Row>

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
            style={{ marginTop: 16 }}
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
                renderItem={({ compose, environmentId, environmentName, projectId, projectName }) => {
                  const notes = degradationNotes(compose, services[compose.id] ?? []);
                  return (
                    <List.Item
                      actions={[
                        <Button key="open" onClick={() => openCompose({ compose, environmentId, environmentName, projectId, projectName })} size="small" type="link">
                          Open <ArrowRightOutlined />
                        </Button>,
                      ]}
                    >
                      <List.Item.Meta
                        description={
                          <Flex align="center" gap={8} wrap>
                            <ComposeStatusTag lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
                            {notes.map((note) => (
                              <Text key={note} type="secondary">
                                {note}
                              </Text>
                            ))}
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
                  sortDirections: ['ascend', 'descend'],
                  sorter: composeComparators.compose,
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
                  sortDirections: ['ascend', 'descend'],
                  sorter: composeComparators.where,
                  title: 'Project / Environment',
                  width: 240,
                },
                {
                  key: 'status',
                  render: (_value, row) => <ComposeStatusInline lastDeployedAt={row.compose.lastDeployedAt} status={row.compose.status} />,
                  sortDirections: ['descend', 'ascend'],
                  sorter: composeComparators.containers,
                  title: 'Containers',
                  width: 200,
                },
                {
                  dataIndex: ['compose', 'updatedAt'],
                  key: 'updated',
                  render: (_value, row) => (
                    <Text type="secondary">
                      <RelativeTime value={row.compose.updatedAt} />
                    </Text>
                  ),
                  defaultSortOrder: 'descend',
                  sortDirections: ['descend', 'ascend'],
                  sorter: composeComparators.updated,
                  title: 'Last edited',
                  width: 160,
                },
                {
                  key: 'deployed',
                  render: (_value, row) =>
                    row.compose.lastDeployedAt ? (
                      <Text type="secondary">
                        <RelativeTime value={row.compose.lastDeployedAt} />
                      </Text>
                    ) : (
                      <Tooltip title="The API does not report deploy times yet (GUIDELINES §11)">
                        <Text type="secondary">—</Text>
                      </Tooltip>
                    ),
                  sortDirections: ['descend', 'ascend'],
                  sorter: composeComparators.deployed,
                  title: 'Last deployed',
                  width: 160,
                },
              ]}
              dataSource={flat}
              onRow={(row) => ({
                className: 'rp-clickable-row',
                onClick: () => openCompose(row),
              })}
              pagination={false}
              rowKey={(row) => row.compose.id}
              size="middle"
            />
          </Card>
        </>
      )}
    </>
  );
}
