import {
  CloudDownloadOutlined,
  CopyOutlined,
  DeleteOutlined,
  DownOutlined,
  EyeOutlined,
  FileTextOutlined,
  FullscreenOutlined,
  LoadingOutlined,
  MoreOutlined,
  ReloadOutlined,
  RedoOutlined,
  RocketOutlined,
  SaveOutlined,
  StopOutlined,
  UndoOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  ConfigProvider,
  Descriptions,
  Dropdown,
  Empty,
  Flex,
  Grid,
  Result,
  Row,
  Select,
  Skeleton,
  Space,
  Spin,
  Switch,
  Tabs,
  Tag,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import type { MenuProps } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useNavigate, useParams } from 'react-router';

import { paths } from '../app/paths';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { ComposeDeploymentsTab } from '../components/ComposeDeploymentsTab';
import { ComposeDomainsTab } from '../components/ComposeDomainsTab';
import { ComposeEditorGuide } from '../components/ComposeEditorGuide';
import { ComposePreviewModal } from '../components/ComposePreviewModal';
import { BuildSnippetsModal, snippetComment } from '../components/BuildSnippetsModal';
import { ComposeTerminalTab } from '../components/ComposeTerminalTab';
import { ComposeVariablesTab } from '../components/ComposeVariablesTab';
import { ComposeVolumesTab } from '../components/ComposeVolumesTab';
import { DangerModal } from '../components/DangerModal';
import { needsRole, RoleHint } from '../components/RoleHint';
import { ZeroDowntimeRefusalModal } from '../components/ZeroDowntimeRefusalModal';
import { DetectDockerfilesModal } from '../components/DetectDockerfilesModal';
import { FullscreenEditor } from '../components/FullscreenEditor';
import {
  CodeValue,
  ComposeStatusTag,
  EmptyState,
  HealthBadge,
  PageHeader,
  RelativeTime,
  ServiceStateTag,
} from '../components/primitives';
import { RemovalPlanList } from '../components/RemovalPlanList';
import { ServiceMonitor } from '../components/ServiceMonitor';
import { SourceCard } from '../components/SourceCard';
import { SourceModal } from '../components/SourceModal';
import { YamlEditor } from '../components/YamlEditor';
import { ApiError, api, removalPlanPath, ZeroDowntimeRefusedError } from '../data/api';
import { useRemovalPlan } from '../data/useRemovalPlan';
import { insertBuildBlocks } from '../data/build-insert';
import { yamlServices } from '../data/source-view';
import { deploymentButtonLabel, isActive } from '../data/deployments-view';
import { usePanel } from '../data/store';
import type { BuildTarget, ComposeVariable, DetectedDockerfile, ComposeVolumes, DeploymentRequest, OrphanVolume, VulnSummary, ZeroDowntimeService } from '../data/types';
import { composeState, findComposeLocation } from '../data/types';
import { effectiveVariableCount } from '../components/variables-view';
import { activeComposeTab, composeDeployIdentity, internalHostnamePattern, composeLinkIsStale, deploySteps, filterLogLines, freshDeployView, hasUnsavedChanges, logServiceOptions } from './compose-view';
import { useComposeSource } from './useComposeSource';
import { useDeploymentHistory } from './useDeploymentHistory';
import { VulnBadges } from '../components/VulnBadges';
import { VulnReportButton } from '../components/VulnReportButton';
import { useCan, useIsAdmin } from '../data/useCan';
import { useVulnSummary } from '../data/useVulnSummary';
import { summaryBadge, vulnLink } from './vulnerabilities-view';
import { buildComposeReport, composeBuilds, reportFilename } from './vuln-report';

const { Text, Paragraph, Title } = Typography;

/**
 * Compose detail (GUIDELINES §4.6–4.7), the port of the prototype's Compose
 * page onto the real store: lifecycle actions are graded by weight (one
 * primary, one "More"), the `Editor | Monitor | Volumes` tab lives in the path
 * so each one is linkable, and the right rail answers "what is running right
 * now, and where does this thing live on the host?" without leaving the screen.
 * The Monitor tab answers how a Service has been behaving, fed by the same
 * Services endpoint as the rail's rows; the Volumes tab lists the Volumes this
 * Compose resolves to and offers each one's tar.gz backup — read-only, and
 * hidden outright when the panel cannot run the feature at all.
 *
 * The page scrolls as one document. The editor sizes itself to its content,
 * between `LAYOUT.editorAutoMinHeight` and `LAYOUT.editorAutoMaxHeight`, and
 * scrolls inside past the cap; the rail keeps its own height beside it.
 */
export function ComposePage() {
  const { composeId: composeParam, environmentId: environmentParam, projectId: projectParam, tab } = useParams();
  const composeId = Number(composeParam);
  const projectId = Number(projectParam);
  const environmentId = Number(environmentParam);
  const navigate = useNavigate();
  const {
    checkedAt,
    deleteCompose,
    deleteOrphanVolume,
    deployCompose,
    isPending,
    loadLogs,
    loadOrphanVolumes,
    loadComposeVariables,
    loadVariables,
    loadServices,
    loadVolumes,
    loadZeroDowntime,
    projects,
    refreshStatus,
    restartCompose,
    saveCompose,
    services: servicesByCompose,
    stopCompose,
  } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();

  const location = findComposeLocation(projects, composeId);
  const compose = location?.compose;
  const environment = location?.environment;
  const project = location?.project;
  // Convenience only: the API decides (ADR-0031).
  const canOperate = useCan(environment?.id, 'operator');
  const canEdit = useCan(environment?.id, 'editor');
  const isAdmin = useIsAdmin();

  const [draft, setDraft] = useState(compose?.yamlContent ?? '');
  const [fullscreenOpen, setFullscreenOpen] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  // Below sm the editor header has room for icons only.
  const screens = Grid.useBreakpoint();
  const [logsText, setLogsText] = useState<string>();
  const [logServices, setLogServices] = useState<string[]>([]);
  const { data: vulnSummary } = useVulnSummary({ composeId }, isAdmin);
  const [logLines, setLogLines] = useState(200);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [freshOpen, setFreshOpen] = useState(false);
  // True from the first confirm until the whole save-then-deploy sequence ends:
  // the save step runs under its own pending key, so `isPending('deploy:…')`
  // alone leaves a window for a second click to start a second wipe.
  const [freshRunning, setFreshRunning] = useState(false);
  const freshRunningRef = useRef(false);
  // Action failures stay on this screen as an Alert (GUIDELINES §8) — the
  // control that retries them is right there.
  const [actionError, setActionError] = useState<string>();
  const [zeroDowntimeRefusal, setZeroDowntimeRefusal] = useState<ZeroDowntimeRefusedError['problems']>();
  const fail = (reason: unknown) =>
    setActionError(reason instanceof Error ? reason.message : 'Something went wrong');

  // The Source decides what the header button does (Build & Deploy or Deploy).
  // Loading and failed are not "none": deploying then would pick the wrong mode,
  // so the deploy actions wait for a successful load.
  const sourceProjectId = project?.id;
  const sourceEnvironmentId = environment?.id;
  const { source: currentSource, state: sourceState, error: sourceError, refresh: refreshSource } = useComposeSource(
    sourceProjectId !== undefined && sourceEnvironmentId !== undefined
      ? { composeId, environmentId: sourceEnvironmentId, projectId: sourceProjectId }
      : undefined,
    (text) => { void message.error(`Could not load the Source: ${text}`); },
  );
  const sourceReady = sourceState === 'none' || sourceState === 'present';
  const hasSource = sourceState === 'present';
  const [sourceModal, setSourceModal] = useState<{ mode?: 'git' | 'zip' }>();
  const [detected, setDetected] = useState<DetectedDockerfile[]>();
  const [snippetsOpen, setSnippetsOpen] = useState(false);
  useEffect(() => {
    setSourceModal(undefined);
    setDetected(undefined);
    setSnippetsOpen(false);
  }, [composeId]);

  // The Deployments tab shows `selectedDeployment`, or the newest when unset.
  const [selectedDeployment, setSelectedDeployment] = useState<number>();
  useEffect(() => {
    setSelectedDeployment(undefined);
  }, [composeId]);
  // The one poll of this Compose's Deployments: the header button and the tab
  // both read it, and it runs the post-deploy refresh when one finishes. The
  // callback is read at fire time, so it may close over later declarations.
  const {
    history: deploymentHistory,
    latest: latestDeployment,
    refresh: refreshLatestDeployment,
    running: runningDeployment,
  } = useDeploymentHistory(
    sourceProjectId !== undefined && sourceEnvironmentId !== undefined
      ? { composeId, environmentId: sourceEnvironmentId, projectId: sourceProjectId }
      : undefined,
    () => onDeploymentFinished(),
  );

  // The Volumes tab's resolution is fetched once here: the tab is hidden when
  // the feature cannot run at all (`available: false`), and that is a
  // page-level decision, so the page owns the fetch and hands the result down.
  // State is keyed to its Compose so a slow answer never shows on another one,
  // and the request counter drops the answer outright.
  const [volumes, setVolumes] = useState<{ composeId: number; payload?: ComposeVolumes; error?: string }>();
  const volumesRequest = useRef(0);
  const volumesForCompose = volumes?.composeId === composeId ? volumes : undefined;
  const volumesPayload = volumesForCompose?.payload;
  const volumesError = volumesForCompose?.error;

  // The orphan section's list is the page's second Volumes fetch, keyed and
  // guarded the same way. Its failures (409 colliding identity, 502 before the
  // first deploy) stay on the section as the API's own words.
  const [orphans, setOrphans] = useState<{ composeId: number; list?: OrphanVolume[]; error?: string; conflict?: boolean }>();
  const orphansRequest = useRef(0);
  const orphansForCompose = orphans?.composeId === composeId ? orphans : undefined;

  useEffect(() => {
    setDraft(compose?.yamlContent ?? '');
  }, [compose?.id, compose?.yamlContent]);

  useEffect(() => {
    setLogsText(undefined);
    setLogServices([]);
  }, [composeId]);

  // The full screen editor must never outlive the Compose it was opened for.
  useEffect(() => {
    setFullscreenOpen(false);
    setFreshOpen(false);
  }, [composeId]);

  // The Environment card and the full screen editor's rail read the
  // Environment's variables; the tree ships with them unloaded.
  const variablesEnvironmentId = environment?.id;
  useEffect(() => {
    if (variablesEnvironmentId !== undefined) {
      loadVariables(variablesEnvironmentId).catch(fail);
    }
  }, [loadVariables, variablesEnvironmentId]);

  // This Compose's own variables: the Variables tab edits them and the
  // Environment card counts them, so the page owns the list. Keyed to its
  // Compose so a slow answer never shows on another one. Loaded once the
  // deep link has resolved its Compose: the request path needs the tree.
  const [composeVariables, setComposeVariables] = useState<{ composeId: number; list?: ComposeVariable[]; error?: string }>();
  const composeVariablesRequest = useRef(0);
  const variablesComposeId = compose?.id;
  const reloadComposeVariables = useCallback(() => {
    if (variablesComposeId === undefined) {
      return;
    }
    const id = variablesComposeId;
    const mine = ++composeVariablesRequest.current;
    loadComposeVariables(id)
      .then((list) => {
        if (composeVariablesRequest.current === mine) setComposeVariables({ composeId: id, list });
      })
      .catch((reason: unknown) => {
        if (composeVariablesRequest.current === mine) {
          const error = reason instanceof Error ? reason.message : 'Something went wrong';
          setComposeVariables((previous) => ({ composeId: id, error, list: previous?.composeId === id ? previous.list : undefined }));
        }
      });
  }, [loadComposeVariables, variablesComposeId]);
  useEffect(() => {
    reloadComposeVariables();
  }, [reloadComposeVariables]);
  const composeVariablesForCompose = composeVariables?.composeId === composeId ? composeVariables : undefined;
  const variableCount = effectiveVariableCount(environment?.variables ?? [], composeVariablesForCompose?.list ?? []);

  // The URL is the source of truth for navigation, so a stale path (renamed
  // parent, hand-edited URL) is redirected to the canonical one — tab included,
  // or the redirect would drop the operator back on the Editor.
  const activeTab = activeComposeTab(tab);

  // Logs are fetched on demand: opening the tab (or landing on its URL) reads them.
  const logsComposeId = compose?.id;
  const onLogsTab = activeTab === 'logs';
  useEffect(() => {
    if (onLogsTab && logsComposeId !== undefined) {
      loadLogs(logsComposeId).then(setLogsText).catch(fail);
    }
  }, [loadLogs, logsComposeId, onLogsTab]);

  // The zero-downtime switches, read once for the whole page: the Domains
  // tab's card, the editor note and the Fresh volumes modal share them, since
  // each read runs `docker compose config` on the host. Read again after a
  // save, a Domain change or a switch change. The note and the modal degrade
  // to today's copy when the read fails; the card shows the error.
  const [zeroDowntimeStatus, setZeroDowntimeStatus] = useState<{
    composeId: number;
    services?: ZeroDowntimeService[];
    error?: string;
  }>();
  const zeroDowntimeRequest = useRef(0);
  const zeroDowntimeComposeId = compose?.id;
  const composeUpdatedAt = compose?.updatedAt;
  const composeDomainsChangedAt = compose?.domainsChangedAt;
  const reloadZeroDowntime = useCallback(async () => {
    if (zeroDowntimeComposeId === undefined) {
      return;
    }
    const id = zeroDowntimeComposeId;
    const mine = ++zeroDowntimeRequest.current;
    try {
      const services = await loadZeroDowntime(id);
      if (zeroDowntimeRequest.current === mine) {
        setZeroDowntimeStatus({ composeId: id, services });
      }
    } catch (reason: unknown) {
      if (zeroDowntimeRequest.current === mine) {
        const error = reason instanceof Error ? reason.message : 'Could not load the zero-downtime switches';
        // A failed reread keeps the last good list.
        setZeroDowntimeStatus((previous) => ({ composeId: id, error, services: previous?.composeId === id ? previous.services : undefined }));
      }
    }
  }, [loadZeroDowntime, zeroDowntimeComposeId]);
  useEffect(() => {
    void reloadZeroDowntime();
  }, [composeDomainsChangedAt, composeUpdatedAt, reloadZeroDowntime]);
  // `available: false` means the feature cannot run at all — a bare binary, no
  // Docker socket — and the tab is hidden rather than shown dead. A URL that
  // still names it redirects to the Editor along with every other stale link.
  const volumesUnavailable = volumesPayload !== undefined && !volumesPayload.available;
  const canonicalTab =
    activeTab === 'editor' || (activeTab === 'volumes' && volumesUnavailable) ? undefined : activeTab;
  const canonicalPath =
    compose && environment && project
      ? paths.compose(project.id, environment.id, compose.id, canonicalTab)
      : undefined;
  const pathIsStale =
    composeLinkIsStale(location, projectId, environmentId) || (activeTab === 'volumes' && volumesUnavailable);
  useEffect(() => {
    if (pathIsStale && canonicalPath) {
      navigate(canonicalPath);
    }
  }, [canonicalPath, navigate, pathIsStale]);

  const dirty = compose ? hasUnsavedChanges(draft, compose.yamlContent) : false;

  // Per-Service rows come from the Services endpoint (the container
  // collector's cache) — the same rows the Monitor tab's picker reads.
  const services = servicesByCompose[composeId] ?? [];

  const fetchServices = useCallback(() => {
    loadServices(composeId).catch((reason: unknown) => {
      setActionError(reason instanceof Error ? reason.message : 'Something went wrong');
    });
  }, [composeId, loadServices]);

  // One resolution per page load (the spec: ~30 ms through the Compose CLI),
  // refetched on Retry and after a deploy writes the env file. A retry clears
  // the payload, so the tab shows its skeleton rather than a stale list.
  const fetchVolumes = useCallback(() => {
    const request = ++volumesRequest.current;
    setVolumes({ composeId });
    return loadVolumes(composeId)
      .then((payload) => {
        if (volumesRequest.current === request) {
          setVolumes({ composeId, payload });
        }
      })
      .catch((reason: unknown) => {
        if (volumesRequest.current === request) {
          setVolumes({
            composeId,
            error: reason instanceof Error ? reason.message : 'Could not resolve this Compose’s Volumes',
          });
        }
      });
  }, [composeId, loadVolumes]);

  // The rows follow the containers: load once the deep link has resolved its
  // Compose (the tree ships later than this page on a cold URL — the same gate
  // the variables load uses), and again whenever the host may have changed.
  const knownComposeId = compose?.id;
  useEffect(() => {
    if (knownComposeId !== undefined) {
      fetchServices();
    }
  }, [fetchServices, knownComposeId]);

  // The Volumes tab cannot ask for its own data — whether the tab exists at
  // all depends on the answer — so the page loads it once per Compose, behind
  // the same deep-link gate as the Services rows.
  // The Volume Explorer is an Editor's; anyone else is not asked about it.
  useEffect(() => {
    if (knownComposeId !== undefined && canEdit) {
      void fetchVolumes();
    }
  }, [canEdit, fetchVolumes, knownComposeId]);

  const fetchOrphans = useCallback(() => {
    const request = ++orphansRequest.current;
    setOrphans({ composeId });
    return loadOrphanVolumes(composeId)
      .then((list) => {
        if (orphansRequest.current === request) {
          setOrphans({ composeId, list });
        }
      })
      .catch((reason: unknown) => {
        if (orphansRequest.current === request) {
          setOrphans({
            composeId,
            error: reason instanceof Error ? reason.message : 'Could not list this Compose’s orphan volumes',
            // 409 is the legacy identity collision: it never clears on retry.
            conflict: reason instanceof ApiError && reason.status === 409,
          });
        }
      });
  }, [composeId, loadOrphanVolumes]);

  useEffect(() => {
    if (knownComposeId !== undefined && canEdit) {
      void fetchOrphans();
    }
  }, [canEdit, fetchOrphans, knownComposeId]);

  const deletePlan = useRemovalPlan(
    deleteOpen && compose && environment && project
      ? removalPlanPath.compose({ composeId: compose.id, environmentId: environment.id, projectId: project.id })
      : undefined,
  );

  const logsLoading = isPending(`logs:${composeId}`);
  const logLineCount = logsText ? logsText.split('\n').length : 0;
  const visibleLogLines = filterLogLines(logsText, logServices, logLines);

  if (!compose || !environment || !project) {
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
        title="Compose not found"
      />
    );
  }

  const state = composeState(compose.status, compose.lastDeployedAt);
  const deployIdentity = composeDeployIdentity(compose);
  const checked = checkedAt[compose.id];

  const sourcePath = { composeId: compose.id, environmentId: environment.id, projectId: project.id };
  const lastBuilt = deploymentHistory?.find((d) => d.status === 'succeeded' && d.sourceRef !== '');

  // The report names the commit the running images were built from, falling
  // back to the Source's checked-out head before any build.
  const loadVulnReport = async () => {
    const images = (await api.vulnImages())
      .map((image) => ({
        image,
        services: [...new Set(image.users.filter((u) => u.kind === 'service' && u.composeId === compose.id).map((u) => u.service ?? ''))],
      }))
      .filter((i) => i.services.length > 0);
    const withFindings = await Promise.all(
      images.map(async (i) => ({ ...i, findings: i.image.counts ? await api.vulnFindings(i.image.imageId) : [] })),
    );
    let source: string | undefined;
    if (currentSource?.kind === 'git') {
      const commit = lastBuilt?.sourceRef || currentSource.headSha;
      source = commit ? `${currentSource.repoUrl} @ ${commit}` : currentSource.repoUrl;
    } else if (currentSource?.kind === 'zip' && currentSource.zip) {
      source = `uploaded zip ${currentSource.zip.name}`;
    }
    const title = `${project.name} / ${environment.name} / ${compose.name}`;
    return {
      markdown: buildComposeReport({ title, source, builds: composeBuilds(compose.yamlContent), images: withFindings, generatedAt: new Date() }),
      filename: reportFilename(title),
    };
  };

  const openDetect = () => {
    api
      .detectDockerfiles(sourcePath)
      .then(setDetected)
      .catch(fail);
  };

  const insertTargets = (targets: BuildTarget[]) => {
    try {
      setDraft(insertBuildBlocks(draft, targets));
      setDetected(undefined);
    } catch {
      message.error('Fix the YAML in the editor before inserting build blocks');
    }
  };

  const insertSnippet = (snippet: 'args' | 'secrets') => {
    setDraft(`${draft.trimEnd()}\n\n${snippetComment(snippet)}\n`);
    setSnippetsOpen(false);
  };

  const save = async () => {
    try {
      await saveCompose(compose.id, draft);
      message.success('Saved docker-compose.yml');
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  // A deploy mints new containers; the rail's rows follow, and the env file it
  // wrote is what the Volumes resolution needs. A fresh deploy also changes
  // which volumes are orphans.
  const refreshAfterDeploy = () => {
    fetchServices();
    if (canEdit) {
      void fetchVolumes();
      void fetchOrphans();
    }
  };

  // Queues a Deployment, then shows it on the Deployments tab. `fresh` removes
  // the Compose's named volumes first; the save-first steps are identical, so
  // unsaved YAML is what gets deployed. With a Source the default is a build.
  const runDeploy = async (options: { fresh?: boolean; mode?: DeploymentRequest['mode']; noCache?: boolean } = {}) => {
    const { fresh = false, noCache } = options;
    if (!sourceReady) {
      message.warning(
        sourceState === 'failed' ? 'The Source could not be loaded — retry loading it first' : 'Still loading the Source — try again in a moment',
      );
      return;
    }
    const mode = options.mode ?? (hasSource ? 'build' : 'deploy');
    if (fresh) {
      if (freshRunningRef.current) {
        return;
      }
      freshRunningRef.current = true;
      setFreshRunning(true);
    }
    try {
      for (const step of deploySteps(dirty)) {
        if (step === 'save') {
          await saveCompose(compose.id, draft);
        } else {
          const request: DeploymentRequest = fresh
            ? { confirmName: compose.name, freshVolumes: true, mode }
            : noCache
              ? { mode, noCache: true }
              : { mode };
          const number = await deployCompose(compose.id, request);
          message.info(`Deployment #${number} queued`);
          setSelectedDeployment(number);
          refreshLatestDeployment();
          navigate(paths.compose(project.id, environment.id, compose.id, 'deployments'));
        }
      }
    } catch (reason: unknown) {
      if (reason instanceof ZeroDowntimeRefusedError) {
        setZeroDowntimeRefusal(reason.problems);
      } else {
        fail(reason);
      }
    } finally {
      if (fresh) {
        freshRunningRef.current = false;
        setFreshRunning(false);
        setFreshOpen(false);
      }
    }
  };
  const deploy = () => runDeploy();

  // The shown Deployment ended: the containers, the env file and the deploy
  // time may all have changed.
  const onDeploymentFinished = () => {
    refreshAfterDeploy();
    refreshStatus(compose.id).catch(fail);
  };

  // Rejections go to the delete dialog, which shows the message itself (an
  // Alert behind the modal mask would be invisible). A 404 means the list was
  // stale, so it is refreshed while the dialog keeps the error.
  const removeOrphan = async (name: string) => {
    try {
      await deleteOrphanVolume(compose.id, name);
    } catch (reason: unknown) {
      if (reason instanceof ApiError && reason.status === 404) {
        void fetchOrphans();
      }
      throw reason;
    }
    message.success(`Deleted volume ${name}`);
    void fetchOrphans();
  };

  const lifecycle = async (action: 'restart' | 'stop') => {
    try {
      if (action === 'restart') {
        await restartCompose(compose.id);
      } else {
        await stopCompose(compose.id);
      }
      message.success(`${action === 'restart' ? 'Restarted' : 'Stopped'} ${compose.name}`);
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  const removeCompose = async () => {
    try {
      await deleteCompose(compose.id);
      setDeleteOpen(false);
      message.success(`Deleted ${compose.name}`);
      navigate(paths.environment(project.id, environment.id));
    } catch (reason: unknown) {
      fail(reason);
    }
  };

  // The tabs that change the Compose: their controls stay visible but
  // disabled below Editor, and the Alert above the tabs says why.
  const editorOnly = (node: ReactNode) => <ConfigProvider componentDisabled={!canEdit}>{node}</ConfigProvider>;

  // Fresh volumes needs the page's Volumes resolution: it names what will be
  // removed. A never-deployed Compose has none (an error payload) and a hidden
  // tab means the feature cannot run, so the item stays visible but disabled
  // and says why (GUIDELINES §9).
  const { volumes: freshVolumes, refusal: freshRefusal } = freshDeployView(volumesPayload, volumesError);
  const deployMenu: MenuProps['items'] = [
    ...(hasSource
      ? [
          {
            icon: <RocketOutlined />,
            key: 'skip-build',
            label: 'Deploy without building',
            onClick: () => void runDeploy({ mode: 'skip-build' }),
          },
          {
            icon: <RedoOutlined />,
            key: 'no-cache',
            label: 'Build without cache',
            onClick: () => void runDeploy({ mode: 'build', noCache: true }),
          },
        ]
      : []),
    {
      disabled: !canEdit || freshRefusal !== undefined,
      icon: <DeleteOutlined />,
      key: 'deploy-fresh',
      label: (
        <Tooltip placement="left" title={canEdit ? freshRefusal : needsRole('editor')}>
          <span>Deploy with fresh volumes</span>
        </Tooltip>
      ),
      onClick: () => setFreshOpen(true),
    },
  ];

  const moreMenu: MenuProps['items'] = [
    {
      disabled: !canOperate || compose.status.total === 0,
      icon: <RedoOutlined />,
      key: 'restart',
      label: <RoleHint allowed={canOperate} level="operator">Restart containers</RoleHint>,
      onClick: () => void lifecycle('restart'),
    },
    {
      disabled: !canOperate || compose.status.total === 0,
      icon: <StopOutlined />,
      key: 'stop',
      label: <RoleHint allowed={canOperate} level="operator">Stop containers</RoleHint>,
      onClick: () => void lifecycle('stop'),
    },
    { type: 'divider' },
    {
      danger: true,
      disabled: !canEdit,
      icon: <DeleteOutlined />,
      key: 'delete',
      label: <RoleHint allowed={canEdit} level="editor">Delete Compose</RoleHint>,
      onClick: () => setDeleteOpen(true),
    },
  ];

  return (
    <>
      <PageHeader
        description={
          <Flex align="center" gap={6} wrap>
            <span>
              {project.name} / {environment.name}
            </span>
            <span>·</span>
            <span>
              Last edited <RelativeTime value={compose.updatedAt} />
            </span>
            {compose.lastDeployedAt && (
              <>
                <span>·</span>
                <span>
                  Deployed <RelativeTime value={compose.lastDeployedAt} />
                </span>
              </>
            )}
          </Flex>
        }
        extra={
          <>
            <Dropdown menu={{ items: moreMenu }} placement="bottomRight" trigger={['click']}>
              <Button icon={<MoreOutlined />} />
            </Dropdown>
            <Button
              disabled={!dirty || !canEdit}
              icon={<SaveOutlined />}
              loading={isPending(`save-compose:${compose.id}`)}
              onClick={() => void save()}
            >
              Save
            </Button>
            <Space.Compact>
              {latestDeployment && isActive(latestDeployment) ? (
                <Button
                  icon={<LoadingOutlined />}
                  onClick={() => navigate(paths.compose(project.id, environment.id, compose.id, 'deployments'))}
                  type="primary"
                >
                  {deploymentButtonLabel(latestDeployment, runningDeployment)}
                </Button>
              ) : (
                <RoleHint allowed={canOperate} level="operator">
                  <Button
                    icon={<RocketOutlined />}
                    disabled={!sourceReady || !canOperate}
                    loading={isPending(`deploy:${compose.id}`)}
                    onClick={() => void deploy()}
                    type="primary"
                  >
                    {hasSource ? 'Build & Deploy' : dirty ? 'Save & Deploy' : 'Deploy'}
                  </Button>
                </RoleHint>
              )}
              <Dropdown menu={{ items: deployMenu }} placement="bottomRight" trigger={['click']}>
                <Button aria-label="More deploy options" disabled={!sourceReady || !canOperate} icon={<DownOutlined />} type="primary" />
              </Dropdown>
            </Space.Compact>
          </>
        }
        tags={
          <>
            <ComposeStatusTag lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
            <VulnBadges badge={summaryBadge(vulnSummary)} to={vulnLink({ composeId: compose.id })} />
            {vulnSummary?.enabled && <VulnReportButton load={loadVulnReport} size="small" />}
            {dirty && (
              <Tag color="processing" variant="filled">
                Unsaved changes
              </Tag>
            )}
          </>
        }
        title={compose.name}
      />

      {actionError && (
        <Alert
          closable={{ afterClose: () => setActionError(undefined) }}
          title={actionError}
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          type="error"
        />
      )}

      {sourceState === 'failed' && (
        <Alert
          action={
            <Button onClick={refreshSource} size="small">
              Retry
            </Button>
          }
          title={`The Source could not be loaded: ${sourceError ?? ''}. Deploying is disabled until it loads.`}
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          type="warning"
        />
      )}

      {!canEdit && (
        <Alert
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          title="You can view this Compose. Changing it needs the Editor role."
          type="info"
        />
      )}

      <Tabs
        activeKey={activeTab}
        items={[
          {
            children: (
              <Row gutter={[LAYOUT.gutter, LAYOUT.gutter]}>
                <Col lg={15} xs={24}>
                  <Card
                    styles={{ body: { padding: 0 } }}
                    title={
                      <Flex align="center" gap={8} justify="space-between" style={{ width: '100%' }}>
                        <Flex align="center" gap={8}>
                          <FileTextOutlined style={{ color: token.colorTextTertiary }} />
                          <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>docker-compose.yml</Text>
                        </Flex>
                        <Space>
                          {dirty && (
                            <Text type="warning" style={{ fontSize: 12 }}>
                              Modified
                            </Text>
                          )}
                          <Tooltip title="Discard local edits and reload the saved file">
                            <Button
                              aria-label="Discard"
                              disabled={!dirty}
                              icon={<UndoOutlined />}
                              onClick={() => setDraft(compose.yamlContent)}
                              size="small"
                            >
                              {screens.sm && 'Discard'}
                            </Button>
                          </Tooltip>
                          <Tooltip title="Show the file a deploy would write, with the panel's additions">
                            <Button aria-label="Preview" icon={<EyeOutlined />} onClick={() => setPreviewOpen(true)} size="small">
                              {screens.sm && 'Preview'}
                            </Button>
                          </Tooltip>
                          <Tooltip title="Edit docker-compose.yml full screen, with the Environment variables beside it">
                            <Button
                              aria-label="Full screen editor"
                              icon={<FullscreenOutlined />}
                              onClick={() => setFullscreenOpen(true)}
                              size="small"
                              type="text"
                            />
                          </Tooltip>
                        </Space>
                      </Flex>
                    }
                    variant="outlined"
                  >
                    {dirty && (
                      <Alert
                        action={
                          <Space>
                            <Button onClick={() => setDraft(compose.yamlContent)} size="small">
                              Discard
                            </Button>
                            <Button
                              loading={isPending(`save-compose:${compose.id}`)}
                              onClick={() => void save()}
                              size="small"
                              type="primary"
                            >
                              Save
                            </Button>
                          </Space>
                        }
                        banner
                        title="You have unsaved changes. Deploying will save them first."
                        type="warning"
                      />
                    )}
                    <div>
                      <YamlEditor
                        autoHeight={{ max: LAYOUT.editorAutoMaxHeight, min: LAYOUT.editorAutoMinHeight }}
                        onChange={setDraft}
                        readOnly={!canEdit}
                        path={`${project.id}-${environment.id}-${compose.id}.yml`}
                        value={draft}
                      />
                    </div>
                    <ComposeEditorGuide
                      zeroDowntime={zeroDowntimeStatus?.composeId === compose.id ? zeroDowntimeStatus.services : undefined}
                    />
                  </Card>
                </Col>

                <Col lg={9} xs={24}>
                  <Flex gap={LAYOUT.gutterSmall} vertical>
                    <Card
                      extra={
                        <Tooltip title="Re-read container status from the host">
                          <Button
                            icon={<ReloadOutlined />}
                            loading={isPending(`status:${compose.id}`) || isPending(`services:${compose.id}`)}
                            onClick={() => {
                              void refreshStatus(compose.id).catch(fail);
                              fetchServices();
                            }}
                            size="small"
                            type="text"
                          />
                        </Tooltip>
                      }
                      title="Status"
                      variant="outlined"
                    >
                      <Flex align="center" gap={8} justify="space-between" style={{ marginBottom: 12 }}>
                        <ComposeStatusTag lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          {checked ? (
                            <>
                              Checked <RelativeTime value={checked} />
                            </>
                          ) : (
                            'Not checked yet'
                          )}
                        </Text>
                      </Flex>

                      {services.length > 0 ? (
                        <Flex gap={10} vertical>
                          {services.map((service) => (
                            <div key={service.name}>
                              <Flex align="center" gap={6} justify="space-between">
                                <Flex align="center" gap={6}>
                                  <Text strong style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>
                                    {service.name}
                                  </Text>
                                  <HealthBadge health={service.health} />
                                </Flex>
                                <ServiceStateTag service={service} />
                              </Flex>
                              <ServiceVulnBadges name={service.name} summary={vulnSummary} />
                              <Text ellipsis style={{ display: 'block', fontFamily: CODE_FONT_STACK, fontSize: 11 }} type="secondary">
                                {service.image}
                              </Text>
                              {service.ports.length > 0 && (
                                <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 11 }} type="secondary">
                                  {service.ports.join(', ')}
                                </Text>
                              )}
                            </div>
                          ))}
                        </Flex>
                      ) : compose.status.total === 0 ? (
                        <Empty
                          description={<Text type="secondary">No containers on the host for this Compose.</Text>}
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          styles={{ image: { height: 40 } }}
                        >
                          <Button
                            disabled={!sourceReady}
                            icon={<RocketOutlined />}
                            loading={isPending(`deploy:${compose.id}`)}
                            onClick={() => void deploy()}
                            size="small"
                            type="primary"
                          >
                            Deploy now
                          </Button>
                        </Empty>
                      ) : (
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          The collector has not read these containers yet — rows appear at its next sweep, every 30
                          seconds. The counts above are what it reads today.
                        </Text>
                      )}

                      {state === 'partial' && (
                        <Alert
                          description="Open the Logs tab and filter to the service that exited."
                          title="Some containers are not running"
                          showIcon
                          style={{ marginTop: 12 }}
                          type="warning"
                        />
                      )}
                    </Card>

                    <Card title="Deploy identity" variant="outlined">
                      <Paragraph style={{ fontSize: 12 }} type="secondary">
                        Frozen at Compose creation (ADR-0011). Renaming the Project or Environment does not move what is
                        already deployed.
                      </Paragraph>
                      <Flex gap={10} vertical>
                        <div>
                          <Text strong style={{ fontSize: 12 }}>
                            Files on disk
                          </Text>
                          <CodeValue
                            copyable
                            value={deployIdentity.directory}
                          />
                        </div>
                        <div>
                          <Text strong style={{ fontSize: 12 }}>
                            docker compose project
                          </Text>
                          <CodeValue copyable value={deployIdentity.projectName} />
                        </div>
                        <div>
                          <Text strong style={{ fontSize: 12 }}>
                            Internal hostname
                          </Text>
                          <CodeValue value={internalHostnamePattern(deployIdentity.projectName)} />
                          <Text style={{ fontSize: 12 }} type="secondary">
                            How other Composes reach a Service on relist-network: one with a Domain, Traefik labels, or
                            relist-network in its YAML. Preview lists each Service&apos;s aliases.
                          </Text>
                        </div>
                      </Flex>
                    </Card>

                    <Card
                      extra={
                        <Button
                          onClick={() => navigate(paths.environment(project.id, environment.id, 'variables'))}
                          size="small"
                          type="link"
                        >
                          Manage
                        </Button>
                      }
                      title="Environment"
                      variant="outlined"
                    >
                      <Descriptions
                        column={1}
                        items={[
                          { children: environment.name, key: 'name', label: 'Environment' },
                          {
                            children: (
                              <Text>
                                {variableCount} variable
                                {variableCount === 1 ? '' : 's'} applied at deploy time
                              </Text>
                            ),
                            key: 'vars',
                            label: 'Variables',
                          },
                          {
                            children: editorOnly(
                              <Text code style={{ fontSize: 12 }}>
                                {`\${${environment.variables[0]?.key ?? 'KEY'}}`}
                              </Text>
                            ),
                            key: 'usage',
                            label: 'Reference as',
                          },
                        ]}
                        size="small"
                      />
                    </Card>

                    <SourceCard
                      draft={draft}
                      lastBuilt={lastBuilt}
                      onConfigure={() => setSourceModal({})}
                      onShowSnippets={() => setSnippetsOpen(true)}
                      onUploadNew={() => setSourceModal({ mode: 'zip' })}
                      onViewDeployments={() => navigate(paths.compose(project.id, environment.id, compose.id, 'deployments'))}
                      source={currentSource}
                    />
                  </Flex>
                </Col>
              </Row>
            ),
            key: 'editor',
            label: 'Editor',
          },
          {
            children: editorOnly(
              <ComposeVariablesTab
                compose={compose}
                deploying={isPending(`deploy:${compose.id}`)}
                environment={environment}
                loadError={composeVariablesForCompose?.error}
                onChanged={reloadComposeVariables}
                onDeploy={() => void deploy()}
                onError={fail}
                variables={composeVariablesForCompose?.list}
              />
            ),
            key: 'variables',
            label: 'Variables',
          },
          {
            children: editorOnly(
              <ComposeDomainsTab
                compose={compose}
                deploying={isPending(`deploy:${compose.id}`)}
                onDeploy={() => void deploy()}
                onOpenEditor={() => navigate(paths.compose(project.id, environment.id, compose.id))}
                onReloadZeroDowntime={reloadZeroDowntime}
                zeroDowntime={zeroDowntimeStatus?.composeId === compose.id ? zeroDowntimeStatus : undefined}
              />
            ),
            key: 'domains',
            label: 'Domains',
          },
          {
            children: (
              <ComposeDeploymentsTab
                active={activeTab === 'deployments'}
                compose={compose}
                history={deploymentHistory}
                location={{ composeId: compose.id, environmentId: environment.id, projectId: project.id }}
                onSelect={setSelectedDeployment}
                running={runningDeployment}
                selected={selectedDeployment}
              />
            ),
            key: 'deployments',
            label: 'Deployments',
          },
          {
            children: (
              <>
            <Flex align="center" gap={8} style={{ marginBottom: 12 }} wrap>
              <Select
                maxTagCount="responsive"
                mode="multiple"
                onChange={setLogServices}
                options={services.map((service) => ({ label: service.name, value: service.name }))}
                placeholder="All services"
                style={{ minWidth: 240 }}
                value={logServices}
              />
              <Select
                onChange={setLogLines}
                options={[
                  { label: 'Last 50 lines', value: 50 },
                  { label: 'Last 200 lines', value: 200 },
                  { label: 'Last 500 lines', value: 500 },
                ]}
                style={{ width: 150 }}
                value={logLines}
              />
              <Tooltip title="Live streaming needs a websocket log feed — deliberately out of scope for v1">
                <Flex align="center" gap={6}>
                  <Switch disabled size="small" />
                  <Text type="secondary">Follow</Text>
                </Flex>
              </Tooltip>
              <Text type="secondary" style={{ marginInlineStart: 'auto' }}>
                {visibleLogLines.length} of {logLineCount} lines
              </Text>
                <Space>
                  <Button
                    icon={<CopyOutlined />}
                    onClick={() => {
                      void navigator.clipboard.writeText(visibleLogLines.join('\n'));
                      message.success('Copied visible log lines');
                    }}
                    size="small"
                  >
                    Copy
                  </Button>
                  <Tooltip title="Downloading logs is out of scope for v1 — the API returns the last 200 lines only">
                    <Button disabled icon={<CloudDownloadOutlined />} size="small">
                      Download
                    </Button>
                  </Tooltip>
                  <Button
                    icon={<ReloadOutlined />}
                    loading={logsLoading}
                    onClick={() => {
                      void loadLogs(compose.id)
                        .then(setLogsText)
                        .catch(fail);
                    }}
                    size="small"
                  >
                    Refresh
                  </Button>
                </Space>
            </Flex>
    
            {logsLoading ? (
              <Skeleton active paragraph={{ rows: 8 }} title={false} />
            ) : visibleLogLines.length === 0 ? (
              <Empty
                description={<Text type="secondary">No log lines for the selected services.</Text>}
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ) : (
              <pre
                className="rp-logs"
                style={{
                  background: token.colorFillQuaternary,
                  border: `1px solid ${token.colorBorderSecondary}`,
                  borderRadius: token.borderRadius,
                  margin: 0,
                  maxHeight: 'calc(100vh - 320px)',
                  overflow: 'auto',
                }}
              >
                {visibleLogLines.map((line, index) => (
                  <div key={`${index}-${line.slice(0, 24)}`}>{line}</div>
                ))}
              </pre>
            )}
            <Title level={5} style={{ marginTop: 16 }}>
              Logs are fetched on demand
            </Title>
            <Text type="secondary">
              The panel returns the last ~200 lines from <Text code>docker compose logs</Text>. Nothing is streamed or stored.
            </Text>
    
              </>
            ),
            key: 'logs',
            label: 'Logs',
          },
          {
            children: (
              // Keyed by Compose so navigating to another Compose's Terminal
              // remounts it: state, the selectors and the socket all belong to
              // one Compose. Switching tabs within this one does not remount.
              <ComposeTerminalTab
                active={activeTab === 'terminal'}
                compose={compose}
                key={compose.id}
                location={{
                  composeId: compose.id,
                  environmentId: environment.id,
                  projectId: project.id,
                }}
                onOpenEditor={() => navigate(paths.compose(project.id, environment.id, compose.id))}
              />
            ),
            disabled: !canEdit,
            key: 'terminal',
            label: <RoleHint allowed={canEdit} level="editor">Terminal</RoleHint>,
          },
          {
            children: (
              <ServiceMonitor
                compose={compose}
                onOpenEditor={() => navigate(paths.compose(project.id, environment.id, compose.id))}
              />
            ),
            key: 'monitor',
            label: 'Monitor',
          },
          // Hidden outright when the feature cannot run at all — a bare binary
          // or no Docker socket — per the spec's security decision 10.
          ...(volumesUnavailable
            ? []
            : [
                {
                  children: (
                    <ComposeVolumesTab
                      deploying={isPending(`deploy:${compose.id}`)}
                      error={volumesError}
                      explorerHref={(volume) =>
                        paths.volumeExplorer(project.id, environment.id, compose.id, volume.source)
                      }
                      onDeleteOrphan={removeOrphan}
                      onDeploy={() => void deploy()}
                      onRetry={() => void fetchVolumes()}
                      onRetryOrphans={() => void fetchOrphans()}
                      orphans={orphansForCompose?.list}
                      orphansConflict={orphansForCompose?.conflict}
                      orphansError={orphansForCompose?.error}
                      payload={volumesPayload}
                    />
                  ),
                  disabled: !canEdit,
                  key: 'volumes',
                  label: <RoleHint allowed={canEdit} level="editor">Volumes</RoleHint>,
                },
              ]),
        ]}
        onChange={(key) =>
          navigate(paths.compose(project.id, environment.id, compose.id, key === 'editor' ? undefined : key))
        }
      />

      <SourceModal
        initialMode={sourceModal?.mode}
        location={sourcePath}
        onClose={() => setSourceModal(undefined)}
        onChanged={refreshSource}
        onRemoved={refreshSource}
        onSaved={(_saved, firstTime) => {
          refreshSource();
          if (firstTime) {
            openDetect();
          }
        }}
        open={sourceModal !== undefined}
        source={currentSource}
      />
      <DetectDockerfilesModal
        existingServices={yamlServices(draft)}
        items={detected ?? []}
        onClose={() => setDetected(undefined)}
        onInsert={insertTargets}
        onShowSnippets={() => setSnippetsOpen(true)}
        open={detected !== undefined}
      />
      <BuildSnippetsModal onClose={() => setSnippetsOpen(false)} onInsert={insertSnippet} open={snippetsOpen} />

      <ComposePreviewModal
        composeId={compose.id}
        dirty={dirty}
        draft={draft}
        editorPath={`${project.id}-${environment.id}-${compose.id}`}
        onClose={() => setPreviewOpen(false)}
        open={previewOpen}
      />
      <FullscreenEditor
        compose={compose}
        dirty={dirty}
        draft={draft}
        environment={environment}
        onChange={setDraft}
        onClose={() => setFullscreenOpen(false)}
        onDiscard={() => setDraft(compose.yamlContent)}
        onSave={() => void save()}
        open={fullscreenOpen}
        project={project}
        saving={isPending(`save-compose:${compose.id}`)}
      />

      <ZeroDowntimeRefusalModal
        onClose={() => setZeroDowntimeRefusal(undefined)}
        onOpenDomains={() => {
          setZeroDowntimeRefusal(undefined);
          navigate(paths.compose(project.id, environment.id, compose.id, 'domains'));
        }}
        onOpenEditor={() => {
          setZeroDowntimeRefusal(undefined);
          navigate(paths.compose(project.id, environment.id, compose.id));
        }}
        problems={zeroDowntimeRefusal}
      />

      <DangerModal
        confirmWord={compose.name}
        consequences={
          <>
            <Alert
              showIcon
              type="warning"
              title="Named volume data is permanently deleted before deployment."
              description="If deployment fails, the deleted data is not restored. Back up the volumes before continuing."
              style={{ marginBottom: LAYOUT.gutterSmall }}
            />
            {freshVolumes.length > 0 ? (
              <>
                <Paragraph style={{ marginBottom: 8 }}>
                  Named volumes this Compose mounts — removed, with everything in them, unless declared{' '}
                  <Text code>external: true</Text>:
                </Paragraph>
                <ul style={{ marginTop: 0 }}>
                  {freshVolumes.map((source) => (
                    <li key={source}>
                      <Text code>{source}</Text>
                    </li>
                  ))}
                </ul>
              </>
            ) : (
              <Paragraph>The deployed file declares no named volumes to list.</Paragraph>
            )}
            <Paragraph>
              Volumes used only by Services behind a Compose profile may also be removed, even if they are not listed. A
              volume with an explicit <Text code>name:</Text> is removed even if another Compose uses it.
            </Paragraph>
            {zeroDowntimeStatus?.composeId === compose.id && zeroDowntimeStatus.services?.some((s) => s.enabled) && (
              <Paragraph>Zero-downtime deploy doesn&apos;t apply to this deploy.</Paragraph>
            )}
            <Paragraph type="secondary">
              Bind mounts are not deleted, and neither are volumes declared <Text code>external: true</Text>. To keep a copy first, download a tar.gz backup from the Volumes tab.
              {dirty ? ' Your unsaved changes are saved first.' : ''}
            </Paragraph>
          </>
        }
        description="Deploy removes this Compose’s named volumes, then deploys it again from empty."
        loading={freshRunning || isPending(`deploy:${compose.id}`)}
        okText="Deploy with fresh volumes"
        onCancel={() => setFreshOpen(false)}
        onConfirm={() => void runDeploy({ fresh: true })}
        open={freshOpen}
        title="Deploy with fresh volumes?"
      />

      <DangerModal
        confirmWord={compose.name}
        consequences={
          <>
            <Paragraph type="secondary">
              This stops and removes the containers and networks created by <Text code>{compose.name}</Text>, then
              deletes its volumes and files as listed here.
            </Paragraph>
            <RemovalPlanList {...deletePlan} fallbackDirectories={[deployIdentity.directory]} />
          </>
        }
        description="Deleting a Compose tears down everything it created on the host."
        loading={isPending(`delete-compose:${compose.id}`)}
        okText="Delete Compose"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={() => void removeCompose()}
        open={deleteOpen}
        title="Delete this Compose?"
      />
    </>
  );
}

/** A Service's own counts in the rail, from the Compose's summary. */
function ServiceVulnBadges({ name, summary }: { name: string; summary?: VulnSummary }) {
  const row = summary?.enabled ? summary.services?.find((s) => s.service === name) : undefined;
  if (!row) {
    return null;
  }
  return (
    <div style={{ marginTop: 4 }}>
      <VulnBadges badge={{ critical: row.counts.critical, high: row.counts.high, fixable: row.counts.fixable }} />
    </div>
  );
}
