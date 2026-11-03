import { AppstoreOutlined, CloudDownloadOutlined, CloudServerOutlined, ContainerOutlined, DashboardOutlined, FolderOutlined, GlobalOutlined, KeyOutlined, MenuOutlined, RocketOutlined, SafetyCertificateOutlined, SafetyOutlined, SyncOutlined } from '@ant-design/icons';
import {
  Alert,
  App,
  Avatar,
  Badge,
  Breadcrumb,
  Button,
  Divider,
  Dropdown,
  Flex,
  Layout,
  Menu,
  Tag,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import type { MenuProps } from 'antd';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Link, Outlet, useLocation, useNavigate } from 'react-router';

import { DiskPressureFooter } from '../components/DiskPressureFooter';
import { PanelVersionFooter } from '../components/PanelVersionFooter';
import { PasswordFromEnvBanner } from '../components/PasswordFromEnvBanner';
import { useDiskPressure } from '../data/useDiskPressure';
import { useGlobalDeploymentQueue } from '../data/useGlobalDeploymentQueue';
import { usePanelUpdateStatus } from '../data/usePanelUpdate';
import { useVulnSummary } from '../data/useVulnSummary';
import { summaryBadge } from '../pages/vulnerabilities-view';
import { usePanel } from '../data/store';
import { composeState, projectComposes, summarize } from '../data/types';
import { COMPOSE_STATE_META, LAYOUT } from './theme';
import { paths, resolvePath } from './paths';

const { Text } = Typography;

/**
 * The shell around every screen (GUIDELINES §6): a 248px sider with the
 * sections and the recent Projects, a 56px sticky header with the right-hand
 * cluster pushed by `marginInlineStart: 'auto'`, and a 24px-padded,
 * 1280px-max content column. The prototype's chrome is not ported: the strip
 * and the latency switch are review scaffolding, the ⌘K palette and dark mode
 * are deliberate v1 omissions. The sider stays the viewport's height so its
 * storage footer remains visible; navigation scrolls when space is limited.
 */
export function AppShell() {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const { auth, loadProjects, projects, refreshStatus, signOut } = usePanel();
  const { data: deploymentQueue } = useGlobalDeploymentQueue(auth.authenticated);
  const diskPressure = useDiskPressure(auth.authenticated);
  const queuedCount = deploymentQueue?.filter((d) => d.status === 'queued').length ?? 0;
  const { data: vulnSummary } = useVulnSummary({}, auth.authenticated);
  const updateStatus = usePanelUpdateStatus(auth.authenticated);
  const updateAvailable = !!updateStatus?.available && !updateStatus.development;
  const criticalCount = summaryBadge(vulnSummary)?.critical ?? 0;
  const [loadError, setLoadError] = useState<string>();
  const [refreshing, setRefreshing] = useState(false);
  const [mobileSidebar, setMobileSidebar] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const closeMobileSidebar = () => { if (mobileSidebar) setSidebarCollapsed(true); };

  // The sider, the breadcrumb and the attention badge all read the Project
  // tree, so the shell loads it once per signed-in session; screens refresh it
  // around their own mutations.
  const loadTree = useCallback(() => {
    setLoadError(undefined);
    return loadProjects().catch((reason: unknown) => {
      setLoadError(reason instanceof Error ? reason.message : 'Something went wrong');
    });
  }, [loadProjects]);

  useEffect(() => {
    if (auth.authenticated) {
      void loadTree();
    }
  }, [auth.authenticated, loadTree]);

  const resolved = useMemo(() => resolvePath(pathname), [pathname]);

  // The section highlighted in the sider: a Project route keeps its Project
  // selected, the sections map onto themselves.
  const selectedKey = resolved
    ? resolved.params.projectId !== undefined
      ? `project-${resolved.params.projectId}`
      : resolved.pattern === 'projects'
        ? 'projects'
        : resolved.pattern === 'monitoring'
          ? 'monitoring'
          : resolved.pattern === 'deployments'
            ? 'deployments'
          : resolved.pattern === 'docker'
            ? 'docker'
          : resolved.pattern === 'panel-update'
            ? 'panel-update'
          : resolved.pattern === 'images'
            ? 'images'
          : resolved.pattern === 'ssh-keys'
            ? 'ssh-keys'
          : resolved.pattern === 'certificates'
            ? 'certificates'
          : resolved.pattern === 'traefik'
            ? 'traefik'
          : resolved.pattern === 'vulnerabilities'
            ? 'vulnerabilities'
          : resolved.pattern === '/'
            ? 'overview'
            : undefined
    : undefined;

  // The badge counts state-based degrades — partly running, or stopped after
  // a successful deploy, dated by the API's `lastDeployedAt` (written on
  // deploy success). Service health refines this on the Overview, whose list
  // loads the per-Service rows the header does not fetch; the definition
  // question stays open in design-port ticket 09.
  const attention = useMemo(
    () =>
      projects.flatMap((project) =>
        projectComposes(project).filter((compose) => {
          const state = composeState(compose.status, compose.lastDeployedAt);
          return state === 'partial' || state === 'stopped';
        }),
      ).length,
    [projects],
  );

  const menuItems: MenuProps['items'] = [
    { key: 'overview', icon: <AppstoreOutlined />, label: 'Overview' },
    { key: 'projects', icon: <FolderOutlined />, label: 'Projects' },
    {
      key: 'deployments', icon: <RocketOutlined />,
      label: <Flex align="center" justify="space-between" gap={8}><span>Deployments</span><Badge count={queuedCount} color={token.colorPrimary} title={`${queuedCount} queued deployments`} /></Flex>,
    },
    { key: 'monitoring', icon: <DashboardOutlined />, label: 'Monitoring' },
    { key: 'docker', icon: <CloudServerOutlined />, label: 'Docker' },
    { key: 'panel-update', icon: <CloudDownloadOutlined />, label: 'Panel update' },
    { key: 'images', icon: <ContainerOutlined />, label: 'Images' },
    {
      key: 'vulnerabilities', icon: <SafetyOutlined />,
      label: <Flex align="center" justify="space-between" gap={8}><span>Vulnerabilities</span>{criticalCount > 0 && <Badge count={criticalCount} color={token.colorError} title={`${criticalCount} Critical vulnerabilities`} />}</Flex>,
    },
    { key: 'ssh-keys', icon: <KeyOutlined />, label: 'SSH keys' },
    { key: 'certificates', icon: <SafetyCertificateOutlined />, label: 'Certificates' },
    { key: 'traefik', icon: <GlobalOutlined />, label: 'Traefik' },
    {
      type: 'group',
      label: 'Recent Projects',
      children: projects.slice(0, 5).map((project) => {
        const composes = projectComposes(project);
        const rollup = summarize(composes);
        const state = composeState(
          { running: rollup.running, total: rollup.total },
          composes.find((compose) => compose.lastDeployedAt)?.lastDeployedAt,
        );
        return {
          key: `project-${project.id}`,
          label: project.name,
          icon: <Badge status={COMPOSE_STATE_META[state].badge} />,
        };
      }),
    },
  ];

  const onMenuClick: MenuProps['onClick'] = ({ key }) => {
    closeMobileSidebar();
    if (key === 'overview') return navigate(paths.overview);
    if (key === 'projects') return navigate(paths.projects);
    if (key === 'monitoring') return navigate(paths.monitoring);
    if (key === 'deployments') return navigate(paths.deployments);
    if (key === 'docker') return navigate(paths.docker);
    if (key === 'panel-update') return navigate(paths.panelUpdate);
    if (key === 'images') return navigate(paths.images);
    if (key === 'ssh-keys') return navigate(paths.sshKeys);
    if (key === 'certificates') return navigate(paths.certificates);
    if (key === 'traefik') return navigate(paths.traefik);
    if (key === 'vulnerabilities') return navigate(paths.vulnerabilities);
    const projectId = Number(String(key).replace('project-', ''));
    if (Number.isFinite(projectId)) void navigate(paths.project(projectId));
  };

  // The trail behind the current screen. The top-level Overview shows none —
  // a single crumb would just repeat the page title (GUIDELINES §3).
  const crumbs = useMemo(() => {
    const trail: { title: ReactNode }[] = [];
    if (!resolved || resolved.params.projectId === undefined) {
      if (resolved?.pattern === 'projects') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Projects' });
      } else if (resolved?.pattern === 'monitoring') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Monitoring' });
      } else if (resolved?.pattern === 'deployments') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Deployments' });
      } else if (resolved?.pattern === 'docker') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Docker' });
      } else if (resolved?.pattern === 'panel-update') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Panel update' });
      } else if (resolved?.pattern === 'images') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Images' });
      } else if (resolved?.pattern === 'ssh-keys') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'SSH keys' });
      } else if (resolved?.pattern === 'certificates') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Certificates' });
      } else if (resolved?.pattern === 'traefik') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Traefik' });
      } else if (resolved?.pattern === 'vulnerabilities') {
        trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: 'Vulnerabilities' });
      }
      return trail;
    }

    const { composeId, environmentId, projectId } = resolved.params;
    const project = projects.find((candidate) => String(candidate.id) === projectId);
    trail.push({ title: <Link to={paths.overview}>Overview</Link> }, { title: <Link to={paths.projects}>Projects</Link> });
    if (environmentId === undefined) {
      trail.push({ title: project?.name ?? 'Unknown Project' });
      return trail;
    }
    trail.push({ title: <Link to={paths.project(projectId)}>{project?.name ?? 'Unknown Project'}</Link> });
    const environment = project?.environments.find((candidate) => String(candidate.id) === environmentId);
    if (composeId === undefined) {
      trail.push({ title: environment?.name ?? 'Unknown Environment' });
      return trail;
    }
    trail.push({
      title: <Link to={paths.environment(projectId, environmentId)}>{environment?.name ?? 'Unknown Environment'}</Link>,
    });
    const compose = environment?.composes.find((candidate) => String(candidate.id) === composeId);
    trail.push({ title: compose?.name ?? 'Unknown Compose' });
    return trail;
  }, [projects, resolved]);

  const refreshAll = async () => {
    setRefreshing(true);
    try {
      await Promise.all(
        projects.flatMap((project) => projectComposes(project).map((compose) => refreshStatus(compose.id))),
      );
    } catch (reason: unknown) {
      message.error(reason instanceof Error ? reason.message : 'Could not refresh statuses');
    } finally {
      setRefreshing(false);
    }
  };

  const accountItems: MenuProps['items'] = [
    { key: 'account', label: 'Account', onClick: () => navigate(paths.account) },
    { type: 'divider' },
    {
      key: 'signout',
      label: 'Sign out',
      onClick: () => {
        void signOut().catch((reason: unknown) => {
          message.error(reason instanceof Error ? reason.message : 'Sign out failed');
        });
      },
    },
  ];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      {mobileSidebar && !sidebarCollapsed && (
        <button
          aria-label="Close navigation backdrop"
          onClick={closeMobileSidebar}
          style={{ position: 'fixed', inset: 0, zIndex: 19, border: 0, padding: 0, background: token.colorBgMask }}
          type="button"
        />
      )}
      <Layout.Sider
        breakpoint="lg"
        collapsed={sidebarCollapsed}
        collapsedWidth={0}
        onBreakpoint={setMobileSidebar}
        onCollapse={setSidebarCollapsed}
        style={{ borderInlineEnd: `1px solid ${token.colorBorderSecondary}`, height: '100dvh', position: mobileSidebar ? 'fixed' : 'sticky', insetInlineStart: mobileSidebar ? 0 : undefined, top: 0, alignSelf: 'flex-start', zIndex: 20 }}
        styles={{ body: { display: 'flex', flexDirection: 'column', minHeight: 0, overflow: 'hidden' } }}
        theme="light"
        trigger={<Button aria-label={sidebarCollapsed ? 'Open navigation' : 'Close navigation'} icon={<MenuOutlined />} style={{ width: '100%', height: '100%' }} type="text" />}
        width={LAYOUT.siderWidth}
      >
        <Flex
          align="center"
          gap={10}
          style={{ borderBottom: `1px solid ${token.colorBorderSecondary}`, height: LAYOUT.barHeight, flexShrink: 0, padding: '0 16px' }}
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
          <Text strong>Relist Panel</Text>
        </Flex>
        <nav aria-label="Main navigation" style={{ flex: 1, minHeight: 0, overflow: 'auto', paddingBottom: token.paddingXS }}>
          <Menu
            items={menuItems}
            mode="inline"
            onClick={onMenuClick}
            selectedKeys={selectedKey ? [selectedKey] : []}
            style={{ borderInlineEnd: 0, paddingTop: 8 }}
          />
        </nav>
        <DiskPressureFooter onNavigate={closeMobileSidebar} pressure={diskPressure} />
        <PanelVersionFooter onNavigate={closeMobileSidebar} version={updateStatus?.version} />
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
            {/* `marginInlineStart: 'auto'` rather than relying on `space-between`:
                on routes with a single crumb the Breadcrumb is absent, and a lone
                child of `space-between` sits at the start — the prototype parked
                the cluster on the left of the Overview header that way. The
                cluster's right edge lands on the 24px gutter on every route. */}
            <Flex align="center" gap={8} style={{ marginInlineStart: 'auto' }}>
              {updateStatus && (
                <Flex align="center" gap={8}>
                  <Text type="secondary">{updateStatus.version}</Text>
                  {updateAvailable && (
                    <Link to={paths.panelUpdate}>
                      <Tag color="processing" style={{ cursor: 'pointer', marginInlineEnd: 0 }}>Update available</Tag>
                    </Link>
                  )}
                </Flex>
              )}
              <Tooltip title="Re-read container status from the host">
                <Badge count={attention} offset={[-4, 4]} size="small">
                  <Button
                    aria-label="Refresh statuses"
                    icon={<SyncOutlined />}
                    loading={refreshing}
                    onClick={() => void refreshAll()}
                  />
                </Badge>
              </Tooltip>
              <Divider style={{ height: 24, margin: 0 }} type="vertical" />
              <Dropdown menu={{ items: accountItems }} placement="bottomRight">
                <Button
                  aria-label="Account"
                  icon={
                    <Avatar size={22} style={{ background: token.colorPrimary }}>
                      A
                    </Avatar>
                  }
                  style={{ paddingInline: 8 }}
                />
              </Dropdown>
            </Flex>
          </Flex>
        </Layout.Header>

        <Layout.Content style={{ padding: LAYOUT.gutter }}>
          <div style={{ margin: '0 auto', maxWidth: LAYOUT.contentMaxWidth }}>
            {loadError && (
              <Alert
                action={<Button onClick={() => void loadTree()}>Retry</Button>}
                title={loadError}
                showIcon
                style={{ marginBottom: LAYOUT.gutter }}
                type="error"
              />
            )}
            <PasswordFromEnvBanner />
            <Outlet />
          </div>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
