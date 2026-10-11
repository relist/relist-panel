import {
  CheckCircleFilled,
  ClockCircleOutlined,
  CloseCircleFilled,
  LoadingOutlined,
  MinusCircleFilled,
} from '@ant-design/icons';
import { Alert, App, Button, Card, Col, Empty, Flex, Row, Steps, Typography, theme } from 'antd';
import dayjs from 'dayjs';
import { useEffect, useRef, useState } from 'react';

import { LAYOUT } from '../app/theme';
import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import { deploymentHeadline, isActive, stepLabel } from '../data/deployments-view';
import type { ActiveDeployment } from '../data/deployments-view';
import type { Compose, Deployment, DeploymentStatus } from '../data/types';
import { RelativeTime } from './primitives';

const { Text } = Typography;

/** How often the log is read while the shown Deployment is active. */
const POLL_MS = 1000;

const STATUS_ICON: Record<DeploymentStatus, React.ReactNode> = {
  canceled: <MinusCircleFilled />,
  failed: <CloseCircleFilled style={{ color: 'var(--ant-color-error)' }} />,
  queued: <ClockCircleOutlined />,
  running: <LoadingOutlined />,
  succeeded: <CheckCircleFilled style={{ color: 'var(--ant-color-success)' }} />,
};

function duration(d: Deployment): string | undefined {
  if (!d.startedAt || !d.finishedAt) {
    return undefined;
  }
  const seconds = Math.max(0, dayjs(d.finishedAt).diff(dayjs(d.startedAt), 'second'));
  return seconds >= 60 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${seconds}s`;
}

/** Steps for one Deployment: which one is current, and how a finished one ended. */
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

/**
 * The Deployments tab: the Compose's last 20 Deployments on the left; on the
 * right the selected one's steps, warnings, failure reason and live log. The
 * page owns the history poll; this tab only follows the shown log.
 */
export function ComposeDeploymentsTab({
  compose,
  history,
  location,
  onSelect,
  running,
  selected,
}: {
  compose: Compose;
  /** The Compose's Deployments, newest first; the page polls them. */
  history?: Deployment[];
  location: ComposePath;
  /** What holds the queue, for the "Waiting — N ahead" line. */
  running?: ActiveDeployment;
  onSelect: (number: number) => void;
  /** The Deployment to show; the newest when unset. */
  selected?: number;
}) {
  const { message } = App.useApp();
  const { token } = theme.useToken();

  const [log, setLog] = useState('');
  const [canceling, setCanceling] = useState(false);

  const composeId = compose.id;
  const shownNumber = selected ?? history?.[0]?.number;
  const shown = history?.find((d) => d.number === shownNumber);

  // The log of the shown Deployment: chunks appended in order until `done`.
  useEffect(() => {
    if (shownNumber === undefined) {
      return;
    }
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let offset = 0;
    setLog('');
    const read = async () => {
      let again = true;
      try {
        const chunk = await api.deploymentLog(location, shownNumber, offset);
        if (stopped) return;
        offset = chunk.offset;
        if (chunk.text) {
          setLog((text) => text + chunk.text);
        }
        again = !chunk.done;
      } catch {
        // Retried on the next tick.
      }
      if (!stopped && again) {
        timer = setTimeout(() => void read(), POLL_MS);
      }
    };
    void read();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [composeId, location.environmentId, location.projectId, shownNumber]);

  // The panel follows the tail as the log grows.
  const logRef = useRef<HTMLPreElement>(null);
  useEffect(() => {
    const el = logRef.current;
    if (el) {
      el.scrollTop = el.scrollHeight;
    }
  }, [log]);

  const cancel = async () => {
    if (!shown) return;
    setCanceling(true);
    try {
      await api.cancelDeployment(location, shown.number);
    } catch (reason: unknown) {
      const text = reason instanceof Error ? reason.message : 'Could not cancel';
      // 409: the run reached the Deploy step meanwhile and can no longer be
      // canceled. That is news, not a failure.
      if ((reason as { status?: number } | null)?.status === 409) {
        message.info(text);
      } else {
        message.error(text);
      }
    } finally {
      setCanceling(false);
    }
  };

  if (history && history.length === 0) {
    return (
      <Empty
        description="No deployments yet. Deploy this Compose to see its steps and log here."
        image={Empty.PRESENTED_IMAGE_SIMPLE}
      />
    );
  }

  const view = shown ? stepsView(shown) : undefined;

  return (
    <Row gutter={[LAYOUT.gutter, LAYOUT.gutter]}>
      <Col lg={8} xs={24}>
        <Card loading={history === undefined} size="small" title="History" variant="outlined">
          <Flex vertical>
            {history?.map((d) => (
              <Button
                block
                key={d.number}
                onClick={() => onSelect(d.number)}
                style={{ height: 'auto', padding: '8px 12px', textAlign: 'start' }}
                type={d.number === shownNumber ? 'default' : 'text'}
              >
                <Flex gap={4} vertical>
                  <Flex align="center" gap={8}>
                    {STATUS_ICON[d.status]}
                    <Text strong>#{d.number}</Text>
                    <Text type="secondary">{d.status}</Text>
                  </Flex>
                  {(d.sourceRef || d.sourceSubject) && (
                    <Text ellipsis type="secondary">
                      {[d.sourceRef, d.sourceSubject].filter(Boolean).join(' · ')}
                    </Text>
                  )}
                  <Text style={{ fontSize: 12 }} type="secondary">
                    {duration(d) ? `${duration(d)} · ` : ''}
                    <RelativeTime value={d.queuedAt} />
                  </Text>
                </Flex>
              </Button>
            ))}
          </Flex>
        </Card>
      </Col>
      <Col lg={16} xs={24}>
        {shown && view && (
          <Card
            extra={
              isActive(shown) && (
                <Button
                  danger
                  loading={canceling}
                  onClick={() => void cancel()}
                  size="small"
                >
                  Cancel
                </Button>
              )
            }
            title={`#${shown.number} · ${deploymentHeadline(shown, running)}`}
            variant="outlined"
          >
            <Flex gap={LAYOUT.gutterSmall} vertical>
              <Steps
                current={view.current}
                items={shown.steps.map((id) => ({ title: stepLabel(id) }))}
                size="small"
                status={view.status}
              />
              {shown.warnings.length > 0 && (
                <Alert
                  showIcon
                  title={
                    <Flex gap={4} vertical>
                      {shown.warnings.map((warning) => (
                        <span key={warning}>{warning}</span>
                      ))}
                    </Flex>
                  }
                  type="warning"
                />
              )}
              {shown.status === 'failed' && shown.reason && <Alert showIcon title={shown.reason} type="error" />}
              <pre
                className="rp-logs"
                data-testid="deployment-log"
                ref={logRef}
                style={{
                  background: token.colorFillQuaternary,
                  border: `1px solid ${token.colorBorderSecondary}`,
                  borderRadius: token.borderRadius,
                  margin: 0,
                  maxHeight: 'calc(100vh - 380px)',
                  minHeight: 160,
                  overflow: 'auto',
                }}
              >
                {log}
              </pre>
            </Flex>
          </Card>
        )}
      </Col>
    </Row>
  );
}
