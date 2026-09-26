import {
  AppstoreOutlined,
  BulbOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  FileTextOutlined,
  FolderOutlined,
  MoonOutlined,
  PictureOutlined,
  RocketOutlined,
  SearchOutlined,
  SunOutlined,
  SyncOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import {
  Avatar,
  Badge,
  Breadcrumb,
  Button,
  Divider,
  Dropdown,
  Flex,
  Layout,
  Menu,
  Segmented,
  Switch,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import type { MenuProps } from 'antd';
import type { ReactNode } from 'react';
import { LAYOUT } from '../app/theme';
import type { ThemeMode } from '../app/theme';
import { useUiState } from '../app/uiState';
import { navigate, paths, useRoute } from '../app/router';
import { usePanel } from '../data/store';
import type { Speed } from '../data/store';
import { composeState, projectComposes, summarize } from '../data/types';
import { CommandPalette } from '../components/CommandPalette';

const { Text } = Typography;

const STATE_DOT = { running: 'success', partial: 'warning', stopped: 'default', 'not-deployed': 'default' } as const;

export function AppShell({
  children,
  onToggleTheme,
  themeMode,
}: {
  children: ReactNode;
  onToggleTheme: () => void;
  themeMode: ThemeMode;
}) {
  const { token } = theme.useToken();
  const route = useRoute();
  const { projects, host, refreshStatus, setSpeed, speed } = usePanel();
  const [paletteOpen, setPaletteOpen] = useUiState('shell.paletteOpen', false);
  const [refreshing, setRefreshing] = useUiState('shell.refreshing', false);

  const selectedKey =
    route.kind === 'project' || route.kind === 'compose' ? `project-${route.projectId}` : route.kind;

  const attention = projects.flatMap((project) =>
    projectComposes(project).filter((compose) => composeState(compose.status, compose.lastDeployedAt) === 'partial'),
  ).length;

  const menuItems: MenuProps['items'] = [
    { key: 'overview', icon: <AppstoreOutlined />, label: 'Overview' },
    { key: 'projects', icon: <FolderOutlined />, label: 'Projects' },
    { key: 'monitoring', icon: <DashboardOutlined />, label: 'Monitoring' },
    {
      type: 'group',
      label: 'Recent Projects',
      children: projects.slice(0, 5).map((project) => {
        const rollup = summarize(projectComposes(project));
        const state = composeState(
          { running: rollup.running, total: rollup.total },
          projectComposes(project).find((compose) => compose.lastDeployedAt)?.lastDeployedAt,
        );
        return {
          key: `project-${project.id}`,
          label: project.name,
          icon: <Badge status={STATE_DOT[state]} />,
        };
      }),
    },
    { type: 'divider' },
    {
      type: 'group',
      label: 'Design system',
      children: [
        { key: 'screens', icon: <PictureOutlined />, label: 'All screens' },
        { key: 'guidelines', icon: <FileTextOutlined />, label: 'Guidelines' },
        { key: 'states', icon: <RocketOutlined />, label: 'States gallery' },
      ],
    },
  ];

  const onMenuClick: MenuProps['onClick'] = ({ key }) => {
    if (key === 'overview') return navigate(paths.overview);
    if (key === 'projects') return navigate(paths.projects);
    if (key === 'monitoring') return navigate(paths.monitoring);
    if (key === 'screens') return navigate(paths.screens);
    if (key === 'guidelines') return navigate(paths.guidelines);
    if (key === 'states') return navigate(paths.states);
    const projectId = Number(String(key).replace('project-', ''));
    if (Number.isFinite(projectId)) navigate(paths.project(projectId));
  };

  const crumbs: { href?: string; title: ReactNode }[] = [];
  if (route.kind === 'overview') crumbs.push({ title: 'Overview' });
  if (route.kind === 'projects') crumbs.push({ href: paths.overview, title: 'Overview' }, { title: 'Projects' });
  if (route.kind === 'monitoring') crumbs.push({ href: paths.overview, title: 'Overview' }, { title: 'Monitoring' });
  if (route.kind === 'project') {
    const project = projects.find((candidate) => candidate.id === route.projectId);
    crumbs.push(
      { href: paths.overview, title: 'Overview' },
      { href: paths.projects, title: 'Projects' },
      { title: project?.name ?? 'Unknown Project' },
    );
  }
  if (route.kind === 'compose') {
    const project = projects.find((candidate) => candidate.id === route.projectId);
    const environment = project?.environments.find((candidate) => candidate.id === route.environmentId);
    const compose = environment?.composes.find((candidate) => candidate.id === route.composeId);
    crumbs.push(
      { href: paths.overview, title: 'Overview' },
      { href: paths.projects, title: 'Projects' },
      { href: paths.project(route.projectId), title: project?.name ?? 'Unknown Project' },
      { href: paths.environment(route.projectId, route.environmentId), title: environment?.name ?? 'Unknown Environment' },
      { title: compose?.name ?? 'Unknown Compose' },
    );
  }
  if (route.kind === 'guidelines' || route.kind === 'states') {
    crumbs.push({ title: 'Design system' }, { title: route.kind === 'guidelines' ? 'Guidelines' : 'States gallery' });
  }
  if (route.kind === 'not-found') crumbs.push({ title: 'Not found' });

  const refreshAll = async () => {
    setRefreshing(true);
    try {
      await Promise.all(
        projects.flatMap((project) =>
          projectComposes(project).map((compose) => refreshStatus(compose.id)),
        ),
      );
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <PrototypeStrip setSpeed={setSpeed} speed={speed} />
      <Layout>
        <Layout.Sider
          breakpoint="lg"
          collapsedWidth={0}
          style={{ borderInlineEnd: `1px solid ${token.colorBorderSecondary}` }}
          theme="light"
          width={LAYOUT.siderWidth}
        >
          <Flex
            align="center"
            gap={10}
            style={{
              borderBottom: `1px solid ${token.colorBorderSecondary}`,
              height: LAYOUT.barHeight,
              padding: '0 16px',
            }}
          >
            <div
              style={{
                alignItems: 'center',
                background: token.colorPrimary,
                borderRadius: 6,
                color: token.colorTextLightSolid,
                display: 'flex',
                flex: 'none',
                fontWeight: 600,
                height: 28,
                justifyContent: 'center',
                width: 28,
              }}
            >
              R
            </div>
            <div style={{ minWidth: 0 }}>
              <Text strong style={{ display: 'block', lineHeight: '18px' }}>
                Relist Panel
              </Text>
              <Text style={{ fontSize: 12 }} type="secondary">
                {host.name}
              </Text>
            </div>
          </Flex>
          {/* `--frame-vh` falls back to `1vh` outside a scene scope, so the
              standalone sider is exactly what it was; in a frame it resolves
              against the frame's declared height (ticket 10). The `Layout`'s
              own `minHeight: 100vh` below is deliberately left viewport-based —
              recording, not fixing: a frame clips it, which is what the board
              wants until it says otherwise. */}
          <div
            className="rp-sider-scroll"
            style={{ height: `calc(100 * var(--frame-vh, 1vh) - ${LAYOUT.barHeight}px - 37px)`, paddingBottom: 12 }}
          >
            <Menu
              items={menuItems}
              mode="inline"
              onClick={onMenuClick}
              selectedKeys={[selectedKey]}
              style={{ borderInlineEnd: 0, paddingTop: 8 }}
            />
            <Divider style={{ margin: '8px 0 0' }} />
            <Flex align="center" gap={8} style={{ padding: '8px 20px' }}>
              <CloudServerOutlined style={{ color: token.colorTextTertiary }} />
              <Text style={{ fontSize: 12 }} type="secondary">
                Docker {host.dockerVersion} · Traefik {host.traefikVersion}
              </Text>
            </Flex>
          </div>
        </Layout.Sider>

        <Layout>
          <Layout.Header
            style={{
              borderBottom: `1px solid ${token.colorBorderSecondary}`,
              position: 'sticky',
              top: 0,
              zIndex: 10,
            }}
          >
            <Flex align="center" gap={16} justify="space-between" style={{ height: '100%' }}>
              {/* A single crumb would just repeat the page title below it. */}
              {crumbs.length > 1 && <Breadcrumb items={crumbs} />}
              {/* `marginInlineStart: auto` rather than relying on `space-between`:
                  on routes with a single crumb the Breadcrumb is absent, and a lone
                  child of `space-between` sits at the start — which parked the
                  search/theme/account cluster on the left of the Overview header. */}
              <Flex align="center" gap={8} style={{ marginInlineStart: 'auto' }}>
                <Button icon={<SearchOutlined />} onClick={() => setPaletteOpen(true)}>
                  Search
                  <Text keyboard style={{ fontSize: 11, marginInlineStart: 6 }}>
                    ⌘K
                  </Text>
                </Button>
                <Tooltip title="Re-read container status from the host">
                  <Badge count={attention} offset={[-4, 4]} size="small">
                    <Button aria-label="Refresh statuses" icon={<SyncOutlined />} loading={refreshing} onClick={() => void refreshAll()} />
                  </Badge>
                </Tooltip>
                <Tooltip title="Dark mode is proposed here — ADR-0004 defers it for v1; the tokens already support it">
                  <Flex align="center" gap={6}>
                    {themeMode === 'light' ? <SunOutlined style={{ color: token.colorTextTertiary }} /> : <MoonOutlined style={{ color: token.colorTextTertiary }} />}
                    <Switch
                      checkedChildren={<MoonOutlined />}
                      onChange={onToggleTheme}
                      size="small"
                      unCheckedChildren={<BulbOutlined />}
                      value={themeMode === 'dark'}
                    />
                  </Flex>
                </Tooltip>
                <Divider style={{ height: 24, margin: 0 }} type="vertical" />
                <Dropdown
                  menu={{
                    items: [
                      { disabled: true, key: 'account', label: 'admin@relist.local' },
                      { type: 'divider' },
                      { key: 'signout', label: 'Sign out', onClick: () => navigate(paths.signIn) },
                    ],
                  }}
                  placement="bottomRight"
                >
                  <Button icon={<Avatar size={22} style={{ background: token.colorPrimary }}>A</Avatar>} style={{ paddingInline: 8 }} />
                </Dropdown>
              </Flex>
            </Flex>
          </Layout.Header>

          <Layout.Content style={{ padding: LAYOUT.gutter }}>
            <div style={{ margin: '0 auto', maxWidth: LAYOUT.contentMaxWidth }}>{children}</div>
          </Layout.Content>
        </Layout>
      </Layout>
      <CommandPalette onClose={() => setPaletteOpen(false)} open={paletteOpen} />
    </Layout>
  );
}

/**
 * Prototype-only chrome: changes how slowly the mock store answers so loading
 * and pending states can be reviewed on demand. Not part of the design — it
 * disappears in a real implementation.
 */
function PrototypeStrip({ setSpeed, speed }: { setSpeed: (speed: Speed) => void; speed: Speed }) {
  const { token } = theme.useToken();
  return (
    <Flex
      align="center"
      gap={12}
      justify="space-between"
      style={{
        background: token.colorWarningBg,
        borderBottom: `1px solid ${token.colorWarningBorder}`,
        color: token.colorTextSecondary,
        fontSize: 12,
        minHeight: 37,
        padding: '4px 24px',
      }}
    >
      <Flex align="center" gap={8}>
        <ThunderboltOutlined style={{ color: token.colorWarning }} />
        <span>
          <strong>Prototype.</strong> Mock data only — no API calls. These screens are a design guideline, not an implementation.
        </span>
      </Flex>
      <Flex align="center" gap={8}>
        <span>Mock latency</span>
        <Segmented
          onChange={(value) => setSpeed(value as Speed)}
          options={[
            { label: 'Instant', value: 'instant' },
            { label: 'Normal', value: 'normal' },
            { label: 'Slow', value: 'slow' },
          ]}
          size="small"
          value={speed}
        />
      </Flex>
    </Flex>
  );
}
