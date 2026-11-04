import { Alert, Button, Flex, Modal, Steps, Typography, theme } from 'antd';
import { useEffect, useState } from 'react';

import { ApiError, api } from '../data/api';
import type { TraefikApplyKind, TraefikApplyRun } from '../data/types';
import { APPLY_STEPS, RECOVERY_TEXT, RESTART_DROP, isTerminal, stepIndex, stepStatus } from '../pages/traefik-apply-view';
import { DiffViewer } from './DiffViewer';

const { Text } = Typography;

const POLL_MS = 2000;

/** The host port README's SSH tunnel uses for the panel. */
const TUNNEL_PORT = 8888;

const CHANGED = 'The configuration changed since you opened it. Reload to see the latest.';

const TITLES: Record<TraefikApplyKind, string> = {
  apply: 'Applying Traefik config',
  restart: 'Restarting Traefik',
  restore: 'Restoring a Traefik backup',
};

export interface TraefikReview {
  /** The override to apply. */
  draft: string;
  /** The traefik.yml the draft generates. */
  effective: string;
  /** The traefik.yml on disk now. */
  current: string;
  revision: string;
}

/**
 * Review & Apply and the progress of a Traefik apply (ADR-0030). With a review
 * it shows the diff first; with a run id it follows that run. The page drops
 * while Traefik restarts, so a failed poll means "reconnecting", not an error.
 */
export function TraefikApplyModal({
  onClose,
  onFinished,
  review,
  runId,
}: {
  onClose: () => void;
  /** The run ended and was acknowledged: reload the config. */
  onFinished: () => void;
  review: TraefikReview | null;
  runId: string | null;
}) {
  const { token } = theme.useToken();
  const [id, setId] = useState<string | null>(runId);
  const [run, setRun] = useState<TraefikApplyRun | null>(null);
  const [reconnecting, setReconnecting] = useState(false);
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<string>();
  const [closing, setClosing] = useState(false);

  useEffect(() => setId(runId), [runId]);

  const done = run !== null && run.id === id && isTerminal(run.state);

  useEffect(() => {
    if (id === null || done) return;
    const timer = setInterval(() => {
      api
        .traefikApplyStatus()
        .then((status) => {
          setReconnecting(false);
          if (status && status.id === id) setRun(status);
        })
        .catch(() => setReconnecting(true));
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [id, done]);

  const apply = async () => {
    if (!review) return;
    setApplying(true);
    setApplyError(undefined);
    try {
      const started = await api.applyTraefikConfig(review.draft, review.revision);
      setId(started.id);
    } catch (reason: unknown) {
      if (reason instanceof ApiError && reason.status === 409) setApplyError(CHANGED);
      else setApplyError(reason instanceof Error ? reason.message : 'Could not start the apply');
    } finally {
      setApplying(false);
    }
  };

  const close = async () => {
    if (id === null) {
      onClose();
      return;
    }
    setClosing(true);
    try {
      await api.ackTraefikApply(id);
    } catch {
      // The result opens again on the next load; closing still works.
    } finally {
      setClosing(false);
      onFinished();
      onClose();
    }
  };

  const reviewing = id === null && review !== null;
  const state = run?.id === id ? run.state : 'validating';
  const pre = {
    background: token.colorFillQuaternary,
    border: `1px solid ${token.colorBorderSecondary}`,
    borderRadius: token.borderRadius,
    margin: 0,
    maxHeight: 320,
    overflow: 'auto',
    whiteSpace: 'pre-wrap' as const,
  };

  return (
    <Modal
      closable={reviewing || done}
      destroyOnHidden
      footer={
        reviewing ? (
          <Flex gap={8} justify="flex-end">
            <Button onClick={onClose}>Cancel</Button>
            <Button loading={applying} onClick={() => void apply()} type="primary">
              Apply
            </Button>
          </Flex>
        ) : done ? (
          <Button loading={closing} onClick={() => void close()} type="primary">
            Close
          </Button>
        ) : null
      }
      mask={{ closable: false }}
      onCancel={() => void close()}
      open
      title={reviewing ? 'Review & Apply' : TITLES[run?.kind ?? 'apply']}
      width={reviewing ? 1000 : 640}
    >
      {reviewing ? (
        <Flex gap={12} vertical>
          <Text type="secondary">traefik.yml: on disk now (left) and after Apply (right).</Text>
          <DiffViewer modified={review.effective} original={review.current} />
          <Alert showIcon title={`${RESTART_DROP}.`} type="warning" />
          {applyError && <Alert showIcon title={applyError} type="error" />}
        </Flex>
      ) : (
        <Flex gap={16} vertical>
          <Steps
            current={stepIndex(state)}
            items={APPLY_STEPS.map((title) => ({ title }))}
            size="small"
            status={stepStatus(state)}
          />
          {reconnecting && !done && <Alert showIcon title="Traefik is restarting — reconnecting…" type="info" />}
          {done && run?.state === 'ok' && <Alert showIcon title="Traefik restarted with the new config." type="success" />}
          {done && run?.state === 'refused' && <Alert showIcon title={run.reason ?? 'The apply was refused.'} type="error" />}
          {done && run?.state === 'rolled-back' && (
            <Alert
              description="Traefik is back on the previous files."
              showIcon
              title={run.reason ?? 'Traefik was not healthy, so the previous files were put back.'}
              type="warning"
            />
          )}
          {done && run?.state === 'rollback-failed' && (
            <>
              <Alert showIcon title={run.reason ?? 'Traefik did not come back.'} type="error" />
              <pre style={pre}>{RECOVERY_TEXT(TUNNEL_PORT, run.backup)}</pre>
            </>
          )}
          {done && run?.log && (run.state === 'rolled-back' || run.state === 'rollback-failed') && (
            <pre className="rp-logs" style={pre}>
              {run.log}
            </pre>
          )}
        </Flex>
      )}
    </Modal>
  );
}
