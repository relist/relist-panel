import { CloudServerOutlined, DeleteOutlined, PlusOutlined, ReloadOutlined, RocketOutlined, SnippetsOutlined } from '@ant-design/icons';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Empty,
  Flex,
  Form,
  Input,
  Result,
  Row,
  Skeleton,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
  theme,
} from 'antd';
import { useState } from 'react';
import { DangerModal } from '../components/DangerModal';
import { ComposeStatusTag, EmptyState, PageHeader } from '../components/primitives';

const { Text, Paragraph, Title } = Typography;

/**
 * Every state a screen can be in, on one page: loading, empty, error,
 * destructive and narrow. Reviewers check these here instead of hunting for
 * them on live screens.
 */
export function StatesGalleryPage() {
  const { message, notification } = App.useApp();
  const { token } = theme.useToken();
  const [dangerOpen, setDangerOpen] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);

  return (
    <>
      <PageHeader
        description="The states that decide whether the panel feels trustworthy: nothing spinning forever, no dead ends, no silent failures."
        tags={<Tag color="processing">gallery</Tag>}
        title="States gallery"
      />

      <Row gutter={[16, 16]}>
        <Col lg={12} xs={24}>
          <Card title="Loading · first paint" variant="outlined">
            <Paragraph type="secondary">
              Skeletons mirror the shape of what is coming (cards, rows, a table), so the layout does not jump when data
              lands. Used on the Overview, Projects and Project screens.
            </Paragraph>
            <Card size="small" variant="outlined">
              <Skeleton active avatar paragraph={{ rows: 2 }} />
            </Card>
            <Card size="small" style={{ marginTop: 12 }} variant="outlined">
              <Skeleton active paragraph={{ rows: 3 }} title={false} />
            </Card>
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Loading · actions and refetches" variant="outlined">
            <Paragraph type="secondary">
              Spinners attach to the control that started the work — never a full-page overlay. Tables dim while a refetch
              is in flight.
            </Paragraph>
            <Space wrap>
              <Button loading type="primary">
                Deploying…
              </Button>
              <Button icon={<ReloadOutlined />} loading>
                Refresh
              </Button>
              <Spin size="small" />
              <Spin indicator={<CloudServerOutlined spin style={{ fontSize: 18 }} />} />
            </Space>
            <Table
              columns={[
                { dataIndex: 'name', key: 'name', title: 'Compose' },
                { dataIndex: 'state', key: 'state', title: 'Status', width: 160 },
              ]}
              dataSource={[
                { key: 1, name: 'web', state: '3/3 running' },
                { key: 2, name: 'worker', state: '1/2 running' },
              ]}
              loading
              pagination={false}
              size="small"
              style={{ marginTop: 16 }}
            />
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Empty · nothing yet" variant="outlined">
            <Paragraph type="secondary">
              An empty state names the thing that is missing, explains it in the operator's terms, and offers the action
              that fills it. Three real examples from the app:
            </Paragraph>
            <Card size="small" variant="outlined">
              <EmptyState
                action={
                  <Button icon={<PlusOutlined />} size="small" type="primary">
                    Create a Project
                  </Button>
                }
                description="A Project is the top level of the panel: one application, with its own Environments and Composes."
                title="No Projects yet"
              />
            </Card>
            <Card size="small" style={{ marginTop: 12 }} variant="outlined">
              <EmptyState
                action={
                  <Button icon={<RocketOutlined />} size="small" type="primary">
                    Write the first Compose
                  </Button>
                }
                description="Paste an existing docker-compose.yml, or start from a small Traefik-ready example."
                title="No Composes in production"
              />
            </Card>
            <Card size="small" style={{ marginTop: 12 }} variant="outlined">
              <EmptyState
                action={
                  <Space>
                    <Button icon={<SnippetsOutlined />} size="small">
                      Paste a .env file
                    </Button>
                    <Button icon={<PlusOutlined />} size="small" type="primary">
                      Add a Variable
                    </Button>
                  </Space>
                }
                description="Anything shared by every Compose here — database URLs, secrets, log level, timezone."
                title="No Environment Variables yet"
              />
            </Card>
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Empty · filtered to nothing" variant="outlined">
            <Paragraph type="secondary">
              Filtering to zero results is not the same as having no data: the copy says so, and the filter stays put so
              it can be cleared.
            </Paragraph>
            <Empty description={<Text type="secondary">No Projects match “billing”. Try a different search.</Text>} image={Empty.PRESENTED_IMAGE_SIMPLE} />
            <Table
              columns={[
                { dataIndex: 'key', key: 'key', title: 'Key', width: 200 },
                { dataIndex: 'value', key: 'value', title: 'Value' },
              ]}
              dataSource={[]}
              locale={{ emptyText: <Text type="secondary">No keys match “MAIL_”.</Text> }}
              pagination={false}
              size="small"
            />
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Error · inline, with a way out" variant="outlined">
            <Paragraph type="secondary">
              Failures stay on the screen they belong to and carry the retry. A toast would vanish before the operator
              finished reading it.
            </Paragraph>
            <Alert
              action={
                <Button icon={<ReloadOutlined />} size="small">
                  Retry
                </Button>
              }
              description="The panel could not reach the Docker socket. Deploys will fail until it is back."
              message="Could not read container status"
              showIcon
              type="error"
            />
            <Alert
              description="The deploy stopped after `api`: port 8080 is already allocated by another container."
              message="Deploy failed for my-saas / production / web"
              showIcon
              style={{ marginTop: 12 }}
              type="error"
            />
            <Alert
              message="Saved, but nothing is deployed yet"
              description="Run Deploy to bring these containers up."
              showIcon
              style={{ marginTop: 12 }}
              type="info"
            />
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Error · page level" variant="outlined">
            <Paragraph type="secondary">
              Two full-page failures: the API is unreachable (the panel cannot even answer /api/auth/status), and a route
              that does not exist.
            </Paragraph>
            <Result
              extra={
                <Button icon={<ReloadOutlined />} type="primary">
                  Try again
                </Button>
              }
              status="500"
              subTitle="Check that the panel container is running and that /var/run/docker.sock is mounted."
              title="Could not contact the panel"
            />
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Form validation" variant="outlined">
            <Paragraph type="secondary">
              Field errors appear under the field, in the field's own words. Submit is blocked, not disabled — the
              operator gets told what is wrong instead of guessing.
            </Paragraph>
            <Form layout="vertical" onFinish={() => message.success('Submitted')} requiredMark={false}>
              <Form.Item
                help="Used in deploy identities — renaming does not move deployed stacks."
                label="Environment variable key"
                name="key"
                rules={[
                  { message: 'Enter a key', required: true },
                  { message: 'Use letters, digits and underscores only', pattern: /^[A-Za-z_][A-Za-z0-9_]*$/ },
                ]}
                validateStatus="error"
              >
                <Input placeholder="DATABASE_URL" style={{ maxWidth: 320 }} value="DATABASE-URL" />
              </Form.Item>
              <Button htmlType="submit" type="primary">
                Add Variable
              </Button>
            </Form>
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Feedback · outcomes" variant="outlined">
            <Paragraph type="secondary">
              Messages confirm outcomes in one line and name the thing: “Deployed web”. They never ask a question and never
              carry a decision.
            </Paragraph>
            <Space wrap>
              <Button onClick={() => message.success('Deployed web')} type="primary">
                Success
              </Button>
              <Button onClick={() => message.error('Could not stop worker: container is restarting')}>Error</Button>
              <Button onClick={() => message.info('Status refreshed from the host')}>Info</Button>
              <Button
                onClick={() =>
                  message.warning('Deploy is still running — the YAML you saved is not live yet')
                }
              >
                Warning
              </Button>
              <Button
                icon={<RocketOutlined />}
                onClick={() => {
                  const key = 'deploy-demo';
                  notification.info({
                    description: 'Writing files, joining the shared network, bringing containers up.',
                    duration: 3,
                    key,
                    message: 'Deploying my-saas / production / web',
                    placement: 'bottomRight',
                  });
                  setTimeout(() => {
                    notification.success({
                      description: '3 containers are up. Traefik picked up the labels.',
                      duration: 3,
                      key,
                      message: 'Deploy finished',
                      placement: 'bottomRight',
                    });
                  }, 2_500);
                }}
              >
                Long-running action
              </Button>
            </Space>
            <Alert
              description="Multi-step work (deploy, bulk variable replace) uses a notification keyed to the action so the progress line is replaced by the result, not stacked on top of it."
              message="Why a notification here?"
              showIcon
              style={{ marginTop: 16 }}
              type="info"
            />
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Destructive confirmations" variant="outlined">
            <Paragraph type="secondary">
              Two tiers. A <Text code>Popconfirm</Text> for cheap, recoverable deletes (a single variable). A
              type-the-name modal for anything that removes containers, volumes or files.
            </Paragraph>
            <Space wrap>
              <Button danger icon={<DeleteOutlined />} onClick={() => setConfirmOpen(true)}>
                Delete a Variable (Popconfirm)
              </Button>
              <Button danger icon={<DeleteOutlined />} onClick={() => setDangerOpen(true)} type="primary">
                Delete a Compose (typed)
              </Button>
            </Space>
            <Card size="small" style={{ marginTop: 16 }} variant="outlined">
              <Flex align="center" gap={12} justify="space-between">
                <div>
                  <Text strong>Danger zone pattern</Text>
                  <br />
                  <Text type="secondary">Last card on a Settings tab, error-tinted border, one action.</Text>
                </div>
                <Button danger icon={<DeleteOutlined />} onClick={() => setDangerOpen(true)}>
                  Delete
                </Button>
              </Flex>
            </Card>
          </Card>
        </Col>

        <Col lg={12} xs={24}>
          <Card title="Narrow viewports" variant="outlined">
            <Paragraph type="secondary">
              The panel is a desktop tool, but it gets opened on a phone after a deploy notification. Below 992px the sider
              collapses to zero, page actions wrap under the title, tables scroll horizontally, and the Compose rail stacks
              under the editor.
            </Paragraph>
            <div
              style={{
                border: `1px solid ${token.colorBorderSecondary}`,
                borderRadius: token.borderRadiusLG,
                margin: '0 auto',
                maxWidth: 360,
                overflow: 'hidden',
                padding: 12,
              }}
            >
              <Flex align="center" gap={8} justify="space-between" style={{ marginBottom: 12 }}>
                <Text strong>web</Text>
                <ComposeStatusTag lastDeployedAt="2026-01-01T00:00:00Z" status={{ running: 2, total: 2 }} />
              </Flex>
              <Flex gap={8} vertical>
                <Button block icon={<RocketOutlined />} type="primary">
                  Deploy
                </Button>
                <Button block>Logs</Button>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  Status rail moves below the editor; the primary action stays visible in the header.
                </Text>
              </Flex>
            </div>
          </Card>
        </Col>
      </Row>

      <Card style={{ marginTop: 16 }} title="Screen inventory" variant="outlined">
        <Table
          columns={[
            { dataIndex: 'screen', key: 'screen', title: 'Screen', width: 220 },
            { dataIndex: 'states', key: 'states', title: 'States covered' },
            { dataIndex: 'path', key: 'path', title: 'Path (mock router)' },
          ]}
          dataSource={[
            { key: 1, path: '#/', screen: 'Overview', states: 'loading · attention empty/all-clear · error banner' },
            { key: 2, path: '#/projects', screen: 'Projects', states: 'loading · empty · filtered-empty · cards/table' },
            { key: 3, path: '#/projects/1', screen: 'Project', states: 'no environments · no composes · tabs' },
            { key: 4, path: '#/projects/1/env/1/variables', screen: 'Variables', states: 'empty · masked values · bulk paste · duplicates' },
            { key: 5, path: '#/projects/1/env/1/compose/1', screen: 'Compose', states: 'dirty · not deployed · partial · logs loading/empty · delete' },
            { key: 6, path: '#/projects/1/env/1/compose/3', screen: 'Compose (never deployed)', states: 'not-deployed tag · empty service list · Deploy now' },
            { key: 7, path: '#/projects/1/env/1/compose/2', screen: 'Compose (partly running)', states: 'warning tag · exited service · attention alert' },
            { key: 8, path: '#/signin · #/setup', screen: 'Auth', states: 'sign in · setup step 1 · setup done' },
            { key: 9, path: '#/nope', screen: 'Not found', states: '404 with escape routes' },
          ]}
          pagination={false}
          size="small"
        />
      </Card>

      <DangerModal
        confirmWord="web"
        consequences={
          <Paragraph type="secondary">
            This stops and removes containers, networks and volumes created by <Text code>web</Text>, then deletes its files
            from <Text code>dataDir/my-saas/production/web/</Text>.
          </Paragraph>
        }
        description="Deleting a Compose tears down everything it created on the host."
        okText="Delete Compose"
        onCancel={() => setDangerOpen(false)}
        onConfirm={() => {
          setDangerOpen(false);
          message.success('Deleted web');
        }}
        open={dangerOpen}
        title="Delete this Compose?"
      />

      <DangerModal
        confirmWord="DATABASE_URL"
        description="Deleting an Environment Variable removes it from the next deploy of every Compose in this Environment."
        okText="Delete Variable"
        onCancel={() => setConfirmOpen(false)}
        onConfirm={() => {
          setConfirmOpen(false);
          message.success('Deleted DATABASE_URL');
        }}
        open={confirmOpen}
        title="Delete this Variable?"
      />

      <Card style={{ marginTop: 16 }} title="Notes for reviewers" variant="outlined">
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <li>
            <Text>Use the “Mock latency” switch in the top strip to slow the store down and see loading and pending states.</Text>
          </li>
          <li>
            <Text>
              Empty and error states are also reachable for real: create a Project to see the empty Overview rollup, or open
              the never-deployed Compose at <Text code>#/projects/1/env/1/compose/3</Text>.
            </Text>
          </li>
          <li>
            <Text>Anything here that requires new API fields is listed under “What the API has to grow” in the guidelines.</Text>
          </li>
        </ul>
        <Title level={5} style={{ marginTop: 16 }}>
          Mocked data
        </Title>
        <Text type="secondary">
          Fixtures live in <Text code>src/data/mocks.ts</Text> — four Projects, six Environments, eight Composes with
          deliberately different states (running, partly running, stopped, never deployed).
        </Text>
      </Card>
    </>
  );
}
