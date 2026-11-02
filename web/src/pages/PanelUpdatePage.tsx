import { CheckCircleFilled, CloseCircleFilled } from '@ant-design/icons';
import { Alert, App, Button, Card, Checkbox, Descriptions, Flex, Skeleton, Steps, Switch, Tag, Typography, theme } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import useSWR from 'swr';

import { LAYOUT } from '../app/theme';
import { DiffViewer } from '../components/DiffViewer';
import { PageHeader, RelativeTime } from '../components/primitives';
import { api, ApiError } from '../data/api';
import {
  NO_ANSWER_MS,
  POLL_MS,
  UPDATE_STEPS,
  canUpdate,
  cancelUpdate,
  checkCommands,
  finishedRun,
  isRestartingError,
  isSessionLost,
  navigation,
  pageState,
  restoreCommands,
  splitUnifiedDiff,
  stepIndex,
} from '../data/panel-update';
import type { PanelUpdateRun, PanelUpdateStatus, PreflightGate, PreflightReport } from '../data/panel-update';

const { Link, Paragraph, Text } = Typography;

export const PANEL_UPDATE_KEY = '/api/panel-update/status';
/** Set just before the page reloads onto the new version, so the new page can say it worked. */
export const UPDATED_FLAG = 'relist.panelUpdate.updatedTo';

const message = (reason: unknown, fallback: string) => (reason instanceof Error ? reason.message : fallback);

function Commands({ text }: { text: string }) {
  return (
    <Text code copyable style={{ display: 'block', whiteSpace: 'pre-wrap' }}>
      {text}
    </Text>
  );
}

/** The Panel update page (`/panel-update`): check, preview, apply and follow an update. */
export function PanelUpdatePage() {
  const { modal } = App.useApp();
  const { data: status, error: loadError, mutate, isLoading } = useSWR<PanelUpdateStatus>(PANEL_UPDATE_KEY, api.panelUpdateStatus, {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  });
  const [report, setReport] = useState<PreflightReport>();
  const [gate, setGate] = useState<PreflightGate>();
  const [notice, setNotice] = useState<string>();
  const [cancelling, setCancelling] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [error, setError] = useState<string>();
  const [done, setDone] = useState<string[]>([]);
  const [checking, setChecking] = useState(false);
  const [savingSetting, setSavingSetting] = useState(false);
  const [watchedRunId, setWatchedRunId] = useState<string>();
  const [restartingSince, setRestartingSince] = useState<number>();
  const [now, setNow] = useState(Date.now);
  const [dismissed, setDismissed] = useState<string[]>([]);
  const [updatedTo] = useState(() => {
    try {
      const value = sessionStorage.getItem(UPDATED_FLAG);
      sessionStorage.removeItem(UPDATED_FLAG);
      return value ?? undefined;
    } catch {
      return undefined;
    }
  });
  const reloaded = useRef(false);

  const run = status?.run ?? null;
  const awaitingRun = watchedRunId !== undefined && !(run?.id === watchedRunId && run.terminal);
  const polling = awaitingRun || (run !== null && !run.terminal) || restartingSince !== undefined;

  // A run seen mid-flight is followed to its end, whoever started it.
  useEffect(() => {
    if (run && !run.terminal && watchedRunId !== run.id) setWatchedRunId(run.id);
  }, [run, watchedRunId]);

  // One read every POLL_MS while Updating or Restarting. A 401 means the session
  // is gone; any other failure the panel's restart explains is Restarting.
  useEffect(() => {
    if (!polling) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const next = await api.panelUpdateStatus();
        if (stopped) return;
        setRestartingSince(undefined);
        await mutate(next, { revalidate: false });
      } catch (reason) {
        if (stopped) return;
        if (isSessionLost(reason)) {
          navigation.toSignIn();
          return;
        }
        if (isRestartingError(reason)) setRestartingSince((since) => since ?? Date.now());
      }
      if (!stopped) timer = setTimeout(() => void tick(), POLL_MS);
    };
    timer = setTimeout(() => void tick(), POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [polling, mutate]);

  const waiting = run !== null && !run.terminal && run.state === 'quiescing';
  useEffect(() => {
    if (restartingSince === undefined && !waiting) return;
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(clock);
  }, [restartingSince, waiting]);

  // The run this page followed has ended. A success on the target version
  // reloads the page once, onto the new bundle.
  useEffect(() => {
    if (!status || !run || !run.terminal || run.id !== watchedRunId) return;
    if (run.state === 'succeeded' && status.version === run.toVersion && !reloaded.current) {
      reloaded.current = true;
      try {
        sessionStorage.setItem(UPDATED_FLAG, run.toVersion);
      } catch {
        // The reload still happens; only the confirmation line is lost.
      }
      navigation.reload();
    }
    setWatchedRunId(undefined);
    setReport(undefined);
  }, [status, run, watchedRunId]);

  const cancel = async () => {
    setCancelling(true);
    setError(undefined);
    try {
      await cancelUpdate();
    } catch (reason) {
      setError(message(reason, 'Could not cancel the update'));
    } finally {
      setCancelling(false);
      await mutate();
    }
  };

  const check = async () => {
    setChecking(true);
    setError(undefined);
    setNotice(undefined);
    try {
      await mutate(await api.panelUpdateCheck(), { revalidate: false });
      setReport(undefined);
    } catch (reason) {
      setError(message(reason, 'Could not check for an update'));
    } finally {
      setChecking(false);
    }
  };

  const setCheckEnabled = async (enabled: boolean) => {
    setSavingSetting(true);
    setError(undefined);
    try {
      await mutate(await api.panelUpdateSettings(enabled), { revalidate: false });
    } catch (reason) {
      setError(message(reason, 'Could not save the update setting'));
    } finally {
      setSavingSetting(false);
    }
  };

  const preview = async () => {
    setPreviewing(true);
    setError(undefined);
    setNotice(undefined);
    setDone([]);
    try {
      const preview = await api.panelUpdatePreflight();
      if (preview.report.upToDate) {
        setReport(undefined);
        setNotice('This panel is already up to date.');
        await mutate();
      } else {
        setReport(preview.report);
        setGate(preview.gate);
      }
    } catch (reason) {
      setReport(undefined);
      setError(message(reason, 'Could not preview the update'));
    } finally {
      setPreviewing(false);
    }
  };

  const confirmUpdate = (current: PreflightReport) => {
    modal.confirm({
      title: `Update to ${current.toVersion}?`,
      okText: 'Update',
      cancelText: 'Cancel',
      width: 560,
      content: (
        <Flex gap={8} vertical>
          <Text>
            The panel updates from <Text strong>{current.fromVersion}</Text> to <Text strong>{current.toVersion}</Text> and
            restarts. It rolls itself back if the new version does not answer.
          </Text>
          <Text>
            {current.recreated.length > 0
              ? `Services to be recreated: ${current.recreated.join(', ')}.`
              : 'No Service other than the panel is recreated.'}
          </Text>
          {current.traefikRecreated && (
            <Text type="warning">Traefik is recreated: every Domain drops for a few seconds.</Text>
          )}
          <Text>
            Queued Deployments are cancelled. A running Deployment or scan is waited for, and new ones are refused until
            the update ends. Changes to the panel are refused too.
          </Text>
        </Flex>
      ),
      onOk: async () => {
        setError(undefined);
        try {
          const started = await api.panelUpdateApply(current.id, done);
          setWatchedRunId(started.run);
          await mutate();
        } catch (reason) {
          // A 409 means the files changed since the preview: it has to be run again.
          setError(message(reason, 'Could not start the update'));
          if (reason instanceof ApiError && reason.status === 409) setReport(undefined);
        }
      },
    });
  };

  const retry = useCallback(() => void mutate(), [mutate]);

  if (!status) {
    return (
      <>
        <PageHeader title="Panel update" />
        {loadError ? (
          <Alert action={<Button onClick={retry}>Retry</Button>} showIcon title={message(loadError, 'Could not read the update status')} type="error" />
        ) : (
          isLoading && <Skeleton active />
        )}
      </>
    );
  }

  const restartingMs = restartingSince === undefined ? undefined : now - restartingSince;
  const state = pageState({ status, report, restartingMs, awaitingRun });
  const failed = finishedRun(status);
  const busy = state === 'waiting' || state === 'updating' || state === 'restarting' || state === 'no-answer';
  const result = failed && !dismissed.includes(failed.id) && !busy ? failed : undefined;

  return (
    <Flex gap={LAYOUT.gutter} vertical>
      <PageHeader
        description="Update this panel to the latest release without leaving the browser."
        extra={
          !busy && (
            <Button loading={checking} onClick={() => void check()}>
              Check now
            </Button>
          )
        }
        title="Panel update"
      />
      {updatedTo && !busy && <Alert closable showIcon title={`Updated to ${updatedTo}`} type="success" />}
      {result && <RunResult onClose={() => setDismissed((ids) => [...ids, result.id])} run={result} />}
      {error && <Alert closable onClose={() => setError(undefined)} showIcon title={error} type="error" />}
      {notice && !busy && <Alert closable onClose={() => setNotice(undefined)} showIcon title={notice} type="info" />}
      {(state === 'restarting' || state === 'no-answer') && (
        <RestartingCard elapsedMs={restartingMs ?? 0} noAnswer={state === 'no-answer'} runId={run?.id ?? watchedRunId} />
      )}
      {state === 'waiting' && run && (
        <WaitingCard cancelling={cancelling} elapsedMs={now - run.startedAt * 1000} onCancel={() => void cancel()} run={run} />
      )}
      {state === 'updating' && <UpdatingCard run={run} />}
      {(state === 'up-to-date' || state === 'available' || state === 'manual') && (
        <StatusCard
          manualReport={report}
          onPreview={() => void preview()}
          onToggle={(enabled) => void setCheckEnabled(enabled)}
          previewing={previewing}
          saving={savingSetting}
          state={state}
          status={status}
        />
      )}
      {(state === 'preview' || state === 'refused') && report && (
        <PreviewCard
          done={done}
          gate={gate}
          onChangeDone={setDone}
          onClose={() => setReport(undefined)}
          onPreviewAgain={() => void preview()}
          onUpdate={() => confirmUpdate(report)}
          previewing={previewing}
          report={report}
        />
      )}
    </Flex>
  );
}

function RunResult({ onClose, run }: { onClose: () => void; run: PanelUpdateRun }) {
  if (run.state === 'aborted') {
    // A cancel says "cancelled"; any other reason is the panel's, such as a Migration lock.
    const cancelled = run.error === 'cancelled' || run.error === '';
    return (
      <Alert
        closable
        description="Nothing was written. The panel runs the same version as before."
        onClose={onClose}
        showIcon
        title={cancelled ? 'Update cancelled' : run.error}
        type="info"
      />
    );
  }
  if (run.state === 'rolled-back') {
    return (
      <Alert
        closable
        description={run.error || undefined}
        onClose={onClose}
        showIcon
        title={`Rolled back to ${run.fromVersion}`}
        type="warning"
      />
    );
  }
  if (run.state === 'rollback-failed') {
    return (
      <Alert
        closable
        description={
          <Flex gap={8} vertical>
            <Text>The update failed: {run.error}</Text>
            <Text>The rollback failed too: {run.rollbackError}</Text>
            {run.backupPath && (
              <>
                <Text>
                  The backup is in <Text code>{run.backupPath}</Text>. To restore it by hand, run this on the host:
                </Text>
                <Commands text={restoreCommands(run.backupPath, run.backupFiles)} />
              </>
            )}
          </Flex>
        }
        onClose={onClose}
        showIcon
        title="The rollback failed"
        type="error"
      />
    );
  }
  return (
    <Alert
      closable
      description={
        <Flex gap={8} vertical>
          <Text>
            The Updater stopped before it finished. The panel is running <Text strong>{run.fromVersion}</Text>
            {run.backupPath ? (
              <>
                ; the backup is in <Text code>{run.backupPath}</Text>
              </>
            ) : null}
            . To check by hand, run this on the host:
          </Text>
          <Commands text={checkCommands(run.id)} />
        </Flex>
      }
      onClose={onClose}
      showIcon
      title="The update was interrupted"
      type="warning"
    />
  );
}

function StatusCard({
  manualReport,
  onPreview,
  onToggle,
  previewing,
  saving,
  state,
  status,
}: {
  manualReport?: PreflightReport;
  onPreview: () => void;
  onToggle: (enabled: boolean) => void;
  previewing: boolean;
  saving: boolean;
  state: 'up-to-date' | 'available' | 'manual';
  status: PanelUpdateStatus;
}) {
  const available = status.available;
  return (
    <Card>
      <Flex gap={16} vertical>
        <Descriptions
          column={1}
          items={[
            { key: 'version', label: 'Running version', children: <Text strong>{status.version}</Text> },
            {
              key: 'checked',
              label: 'Last checked',
              children: status.lastCheckAt ? <RelativeTime value={status.lastCheckAt} /> : <Text type="secondary">Never</Text>,
            },
            {
              key: 'daily',
              label: 'Daily check',
              children: (
                <Switch
                  aria-label="Check for an update every day"
                  checked={status.checkEnabled}
                  loading={saving}
                  onChange={onToggle}
                />
              ),
            },
          ]}
          size="small"
        />
        {status.message && <Alert showIcon title={status.message} type="info" />}
        {status.lastError && <Alert showIcon title={`The last check failed: ${status.lastError}`} type="warning" />}
        {state === 'up-to-date' && !status.message && <Text type="secondary">This panel is up to date.</Text>}
        {state === 'available' && available && (
          <Alert
            action={
              <Button loading={previewing} onClick={onPreview} type="primary">
                Preview
              </Button>
            }
            description={
              <Flex gap={4} vertical>
                <Text>
                  Released <RelativeTime value={available.releasedAt} />.{' '}
                  {available.notesUrl && (
                    <Link href={available.notesUrl} rel="noreferrer" target="_blank">
                      Release notes
                    </Link>
                  )}
                </Text>
                <Text type="secondary">Preview shows what the update changes before anything is written.</Text>
              </Flex>
            }
            showIcon
            title={`Version ${available.version} is available`}
            type="info"
          />
        )}
        {state === 'manual' && <ManualNotice report={manualReport} status={status} />}
      </Flex>
    </Card>
  );
}

function ManualNotice({ report, status }: { report?: PreflightReport; status: PanelUpdateStatus }) {
  const readme = report?.readmeUrl;
  return (
    <Alert
      description={
        <Flex gap={4} vertical>
          {report?.needsManualUpdate ? (
            <Text>
              This version uses install checks this panel does not know
              {report.unknownKinds.length > 0 ? `: ${report.unknownKinds.join(', ')}` : ''}.
            </Text>
          ) : (
            <Text>{status.install.reason || 'This Install cannot be updated from the page.'}</Text>
          )}
          <Text>Update it by hand, following the README.</Text>
          {readme && (
            <Link href={readme} rel="noreferrer" target="_blank">
              Upgrading an existing install
            </Link>
          )}
        </Flex>
      }
      showIcon
      title="This Install needs a manual update"
      type="warning"
    />
  );
}

function UpdatingCard({ run }: { run: PanelUpdateRun | null }) {
  const rollingBack = run?.state === 'rolling-back';
  const current = rollingBack ? UPDATE_STEPS.length : run && !run.terminal ? stepIndex(run.state) : 0;
  return (
    <Card title={run ? `Updating to ${run.toVersion}` : 'Updating'}>
      <Flex gap={16} vertical>
        <Steps
          current={current}
          items={[
            ...UPDATE_STEPS.map((step, index) => ({
              title: step.title,
              status: rollingBack && index === UPDATE_STEPS.length - 1 ? ('error' as const) : undefined,
            })),
            ...(rollingBack ? [{ title: 'Roll back to the previous version', status: 'process' as const }] : []),
          ]}
          orientation="vertical"
          size="small"
        />
        {run?.note && <Alert showIcon title={run.note} type="warning" />}
        <Text type="secondary">
          The page follows the update by itself. Do not close the panel's containers while it runs.
        </Text>
      </Flex>
    </Card>
  );
}

function elapsed(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

function WaitingCard({
  cancelling,
  elapsedMs,
  onCancel,
  run,
}: {
  cancelling: boolean;
  elapsedMs: number;
  onCancel: () => void;
  run: PanelUpdateRun;
}) {
  return (
    <Card
      extra={
        <Button danger loading={cancelling} onClick={onCancel}>
          Cancel
        </Button>
      }
      title={`Waiting to update to ${run.toVersion}`}
    >
      <Flex gap={8} vertical>
        <Text>
          {run.waitingOn ? (
            <>
              Waiting for <Text strong>{run.waitingOn}</Text> to finish. Nothing has been changed yet.
            </>
          ) : (
            'Waiting for the panel to go quiet. Nothing has been changed yet.'
          )}
        </Text>
        <Text type="secondary">Waiting {elapsed(elapsedMs)}. Queued Deployments are cancelled; new ones are refused.</Text>
        <Text type="secondary">Cancel ends the update before anything is written.</Text>
      </Flex>
    </Card>
  );
}

function RestartingCard({ elapsedMs, noAnswer, runId }: { elapsedMs: number; noAnswer: boolean; runId?: string }) {
  return (
    <Card title={noAnswer ? 'The panel is not answering' : 'Restarting…'}>
      <Flex gap={16} vertical>
        <Text>
          {noAnswer
            ? `The panel has not answered for ${elapsed(Math.max(elapsedMs, NO_ANSWER_MS))}. The page keeps trying. To see what happened, run this on the host:`
            : `The panel is restarting. Waiting ${elapsed(elapsedMs)}.`}
        </Text>
        {noAnswer && <Commands text={checkCommands(runId ?? '<run id>')} />}
      </Flex>
    </Card>
  );
}

function PreviewCard({
  done,
  gate,
  onChangeDone,
  onClose,
  onPreviewAgain,
  onUpdate,
  previewing,
  report,
}: {
  done: string[];
  gate?: PreflightGate;
  onChangeDone: (ids: string[]) => void;
  onClose: () => void;
  onPreviewAgain: () => void;
  onUpdate: () => void;
  previewing: boolean;
  report: PreflightReport;
}) {
  const { token } = theme.useToken();
  const refused = !!report.failed || !report.ready;
  const changeLabel =
    report.change === 'not-edited'
      ? 'Not edited'
      : report.change === 'edited-merged'
        ? 'Edited, merged'
        : report.change === 'edited-conflict'
          ? 'Edited, conflict'
          : undefined;
  const sides = report.change === 'edited-merged' && report.diff ? splitUnifiedDiff(report.diff) : undefined;

  return (
    <Card title={refused ? 'The update cannot start' : `Update to ${report.toVersion}`}>
      <Flex gap={16} vertical>
        {report.failed && (
          <Alert
            description={<Text style={{ whiteSpace: 'pre-wrap' }}>{report.failed.message}</Text>}
            showIcon
            title={`The ${report.failed.step} step failed`}
            type="error"
          />
        )}
        {!report.failed && !report.ready && (
          <Alert showIcon title="An Install invariant does not hold, so the update would not start." type="error" />
        )}
        <Descriptions
          column={1}
          items={[
            { key: 'from', label: 'Version', children: `${report.fromVersion} to ${report.toVersion}` },
            ...(report.notesUrl
              ? [
                  {
                    key: 'notes',
                    label: 'Release notes',
                    children: (
                      <Link href={report.notesUrl} rel="noreferrer" target="_blank">
                        {report.notesUrl}
                      </Link>
                    ),
                  },
                ]
              : []),
            ...(changeLabel
              ? [
                  {
                    key: 'change',
                    label: 'Install file',
                    children: <Tag color={report.change === 'edited-conflict' ? 'error' : undefined}>{changeLabel}</Tag>,
                  },
                ]
              : []),
            ...(gate
              ? [
                  {
                    key: 'gate',
                    label: 'Right now',
                    children: (
                      <Flex gap={4} wrap>
                        <Tag color={gate.installAvailable ? 'success' : 'error'}>
                          {gate.installAvailable ? 'Install found' : 'Install not found'}
                        </Tag>
                        <Tag color={gate.migrationLocked ? 'error' : 'success'}>
                          {gate.migrationLocked ? 'Migration in progress' : 'No Migration'}
                        </Tag>
                        <Tag color={gate.deploysBusy ? 'warning' : 'success'}>
                          {gate.deploysBusy ? 'Deployments running' : 'No Deployments running'}
                        </Tag>
                      </Flex>
                    ),
                  },
                ]
              : []),
          ]}
          size="small"
        />

        {report.conflict && (
          <Flex gap={8} vertical>
            <Text strong>
              The Install file you edited and the new one conflict in {report.conflict.hunks}{' '}
              {report.conflict.hunks === 1 ? 'place' : 'places'}
            </Text>
            <Text type="secondary">Your file on the left, the new version's file on the right.</Text>
            <DiffViewer modified={report.conflict.theirs} original={report.conflict.ours} />
            <details>
              <summary>The merge, with its conflict markers</summary>
              <pre style={{ overflow: 'auto', margin: '8px 0 0' }}>{report.conflict.merged}</pre>
            </details>
          </Flex>
        )}

        {sides && (
          <Flex gap={8} vertical>
            <Text strong>The Install file after the merge</Text>
            <Text type="secondary">Your edits are kept. This is what changes in the file.</Text>
            <DiffViewer modified={sides.modified} original={sides.original} />
          </Flex>
        )}

        {report.invariants.length > 0 && (
          <Flex gap={4} vertical>
            <Text strong>Install checks</Text>
            {report.invariants.map((invariant) => (
              <Flex align="flex-start" gap={8} key={invariant.kind + invariant.text}>
                {invariant.ok ? (
                  <CheckCircleFilled aria-label="Passes" style={{ color: token.colorSuccess, marginTop: 4 }} />
                ) : (
                  <CloseCircleFilled aria-label="Fails" style={{ color: token.colorError, marginTop: 4 }} />
                )}
                <Flex gap={0} vertical>
                  <Text>{invariant.text}</Text>
                  {!invariant.ok && invariant.detail && <Text type="danger">{invariant.detail}</Text>}
                  {!invariant.ok && invariant.source && (
                    <Text type="secondary">
                      Set in <Text code>{invariant.source}</Text>
                    </Text>
                  )}
                </Flex>
              </Flex>
            ))}
          </Flex>
        )}

        {!refused && (
          <Flex gap={8} vertical>
            <Text strong>Services that will be recreated</Text>
            {report.recreated.length > 0 ? (
              <Flex gap={4} wrap>
                {report.recreated.map((name) => (
                  <Tag key={name}>{name}</Tag>
                ))}
              </Flex>
            ) : (
              <Text type="secondary">Only the panel.</Text>
            )}
            {report.traefikRecreated && (
              <Alert showIcon title="Traefik is recreated: every Domain drops for a few seconds." type="warning" />
            )}
          </Flex>
        )}

        {!refused && report.manualSteps.length > 0 && (
          <Flex gap={8} vertical>
            <Text strong>Before you update</Text>
            <Paragraph style={{ margin: 0 }} type="secondary">
              This release asks you to do these by hand. Tick each when it is done.
            </Paragraph>
            {report.manualSteps.map((step) => (
              <Checkbox
                checked={done.includes(step.id)}
                key={step.id}
                onChange={(event) =>
                  onChangeDone(event.target.checked ? [...done, step.id] : done.filter((id) => id !== step.id))
                }
              >
                {step.text}
              </Checkbox>
            ))}
          </Flex>
        )}

        <Flex gap={8} justify="flex-end" wrap>
          <Button onClick={onClose}>Close</Button>
          <Button loading={previewing} onClick={onPreviewAgain}>
            Preview again
          </Button>
          {!refused && (
            <Button disabled={!canUpdate(report, done)} onClick={onUpdate} type="primary">
              Update
            </Button>
          )}
        </Flex>
      </Flex>
    </Card>
  );
}
