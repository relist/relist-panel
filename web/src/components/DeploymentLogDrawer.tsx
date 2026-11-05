import { Alert, App, Button, Drawer, Flex, Skeleton, Steps, Typography, theme } from 'antd';
import { useEffect, useRef, useState } from 'react';
import { LAYOUT } from '../app/theme';
import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import { deploymentHeadline, isActive, stepLabel } from '../data/deployments-view';
import type { ActiveDeployment } from '../data/deployments-view';
import type { Deployment } from '../data/types';
import { useCan } from '../data/useCan';
import { RoleHint } from './RoleHint';

const { Text } = Typography;
const POLL_MS = 1000;

export type DeploymentLogTarget = {
  location: ComposePath;
  number: number;
  deployment?: Deployment;
};

/** A shared log viewer; queue selections also follow their current status. */
export function DeploymentLogDrawer({ target, onClose, running, follow = false, context, onCanceled }: {
  target?: DeploymentLogTarget;
  onClose: () => void;
  running?: ActiveDeployment;
  follow?: boolean;
  context?: string;
  onCanceled?: () => void;
}) {
  return (
    <Drawer
      destroyOnHidden
      onClose={onClose}
      open={target !== undefined}
      placement="right"
      size="min(880px, 100vw)"
      title={
        <Flex vertical style={{ minWidth: 0 }}>
          <span>Deployment #{target?.number ?? ''}</span>
          {context && <Text ellipsis={{ tooltip: context }} style={{ display: 'block', fontWeight: 400, fontSize: 12 }} type="secondary">{context}</Text>}
        </Flex>
      }
      styles={{ body: { display: 'flex', flexDirection: 'column', minWidth: 0, overflow: 'hidden' }, title: { minWidth: 0 } }}
    >
      {target && <DeploymentDetails key={`${target.location.projectId}/${target.location.environmentId}/${target.location.composeId}/${target.number}`} target={target} follow={follow} running={running} onCanceled={onCanceled} />}
    </Drawer>
  );
}

function stepsView(d: Deployment): { current: number; status: 'process' | 'finish' | 'error' | 'wait' } {
  if (d.status === 'succeeded') {
    return { current: d.steps.length, status: 'finish' };
  }
  if (d.status === 'failed' || d.status === 'canceled') {
    const failedAt = d.steps.indexOf(d.failedStep || d.step);
    return { current: Math.max(failedAt, 0), status: 'error' };
  }
  if (d.status === 'queued') {
    return { current: 0, status: 'wait' };
  }
  return { current: Math.max(d.steps.indexOf(d.step), 0), status: 'process' };
}

function DeploymentDetails({ target, follow, running, onCanceled }: {
  target: DeploymentLogTarget;
  follow: boolean;
  running?: ActiveDeployment;
  onCanceled?: () => void;
}) {
  const { location, number, deployment } = target;
  const [loaded, setLoaded] = useState<Deployment>();
  const [detailError, setDetailError] = useState<string>();
  const [attempt, setAttempt] = useState(0);
  const shown = follow ? loaded ?? deployment : deployment;

  useEffect(() => {
    if (!follow) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const read = async () => {
      let again = true;
      let delay = POLL_MS;
      try {
        const d = await api.getDeployment(location, number);
        if (stopped) return;
        setLoaded(d);
        setDetailError(undefined);
        again = isActive(d);
      } catch (reason) {
        if (stopped) return;
        setDetailError(reason instanceof Error ? reason.message : 'Could not load deployment');
        delay = 5000;
      }
      if (!stopped && again) timer = setTimeout(() => void read(), delay);
    };
    void read();
    return () => { stopped = true; clearTimeout(timer); };
  }, [attempt, follow, location.composeId, location.environmentId, location.projectId, number]);
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [log, setLog] = useState('');
  const [logError, setLogError] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const logRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let offset = 0;
    const read = async () => {
      let again = true;
      try {
        const chunk = await api.deploymentLog(location, number, offset);
        if (stopped) return;
        offset = chunk.offset;
        setLogError(false);
        if (chunk.text) setLog((text) => text + chunk.text);
        again = !chunk.done;
      } catch {
        if (!stopped) setLogError(true);
      }
      if (!stopped && again) timer = setTimeout(() => void read(), POLL_MS);
    };
    void read();
    return () => { stopped = true; clearTimeout(timer); };
  }, [location.composeId, location.environmentId, location.projectId, number]);

  useEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [log]);

  const canCancel = useCan(location.environmentId, 'operator');
  const cancel = async () => {
    if (!shown) return;
    setCanceling(true);
    try {
      await api.cancelDeployment(location, number);
      onCanceled?.();
      if (follow) setAttempt((n) => n + 1);
    } catch (reason: unknown) {
      const text = reason instanceof Error ? reason.message : 'Could not cancel';
      if ((reason as { status?: number } | null)?.status === 409) message.info(text);
      else message.error(text);
    } finally {
      setCanceling(false);
    }
  };
  const view = shown ? stepsView(shown) : undefined;

  return (
    <Flex gap={LAYOUT.gutterSmall} vertical style={{ flex: 1, minHeight: 0, minWidth: 0, overflowWrap: 'anywhere' }}>
      {detailError && <Alert showIcon title={detailError} type="error" action={<Button onClick={() => setAttempt((n) => n + 1)} size="small">Retry</Button>} />}
      {!shown && !detailError && <Skeleton active paragraph={{ rows: 3 }} title={false} />}
      {shown && view && (
        <Flex gap={12} vertical style={{ maxHeight: '45%', flexShrink: 0, overflow: 'auto', overflowWrap: 'anywhere' }}>
          <Flex align="start" gap={12} justify="space-between">
            <Text strong style={{ minWidth: 0 }}>{deploymentHeadline(shown, running)}</Text>
            {isActive(shown) && (
              <RoleHint allowed={canCancel} level="operator">
                <Button danger disabled={!canCancel} loading={canceling} onClick={() => void cancel()} size="small">Cancel</Button>
              </RoleHint>
            )}
          </Flex>
          {shown.warnings.length > 0 && <Alert showIcon title={<Flex gap={4} vertical>{shown.warnings.map((warning) => <span key={warning}>{warning}</span>)}</Flex>} type="warning" />}
          {(shown.sourceRef || shown.sourceSubject) && <Text type="secondary">{[shown.sourceRef, shown.sourceSubject].filter(Boolean).join(' · ')}</Text>}
          <Steps current={view.current} items={shown.steps.map((id) => ({ title: stepLabel(id) }))} orientation="vertical" size="small" status={view.status} />
          {shown.status === 'failed' && shown.reason && <Alert showIcon title={shown.reason} type="error" />}
        </Flex>
      )}
      {logError && <Alert showIcon title="Could not load logs. Retrying…" type="warning" />}
      <pre
        className="rp-logs"
        data-testid="deployment-log"
        ref={logRef}
        style={{
          background: token.colorFillQuaternary,
          border: `1px solid ${token.colorBorderSecondary}`,
          borderRadius: token.borderRadius,
          flex: 1,
          margin: 0,
          minHeight: 0,
          minWidth: 0,
          overflow: 'auto',
          overflowWrap: 'anywhere',
        }}
      >{log}</pre>
    </Flex>
  );
}
