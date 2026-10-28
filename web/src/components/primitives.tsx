import { InfoCircleOutlined } from '@ant-design/icons';
import { Badge, Breadcrumb, Card, Flex, Tag, Tooltip, Typography, theme } from 'antd';
import type { ReactNode } from 'react';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import { COMPOSE_STATE_META, CODE_FONT_STACK, LAYOUT, SERVICE_STATE_META } from '../app/theme';
import type { ComposeService, ComposeStatus, ServiceHealth } from '../data/types';
import { composeState } from '../data/types';

dayjs.extend(relativeTime);

const { Text, Title } = Typography;

export function RelativeTime({ value }: { value: string }) {
  return (
    <Tooltip title={dayjs(value).format('D MMM YYYY, HH:mm:ss')}>
      <span>{dayjs(value).fromNow()}</span>
    </Tooltip>
  );
}

/** A card title whose standing explanation waits in an ⓘ tooltip instead of a paragraph under it. */
export function InfoTitle({ info, title }: { info: ReactNode; title: ReactNode }) {
  const { token } = theme.useToken();
  return (
    <Flex align="center" gap={6}>
      {title}
      <Tooltip title={info}>
        <InfoCircleOutlined aria-label="About this card" style={{ color: token.colorTextTertiary, fontSize: 13 }} />
      </Tooltip>
    </Flex>
  );
}

/** The one status vocabulary for a Compose. Tag form — headers, cards, summaries. */
export function ComposeStatusTag({
  lastDeployedAt,
  status,
}: {
  lastDeployedAt?: string | null;
  status: ComposeStatus;
}) {
  const state = composeState(status, lastDeployedAt);
  const meta = COMPOSE_STATE_META[state];
  const count = status.total > 0 ? `${status.running}/${status.total}` : undefined;

  if (state === 'not-deployed') {
    return (
      <Tooltip title={meta.hint}>
        <Tag style={{ borderStyle: 'dashed' }} variant="outlined">
          {meta.label}
        </Tag>
      </Tooltip>
    );
  }

  return (
    <Tooltip title={meta.hint}>
      <Tag color={meta.badge === 'default' ? undefined : meta.badge} variant="filled">
        {count ? `${count} · ${meta.label}` : meta.label}
      </Tag>
    </Tooltip>
  );
}

/** Dot form — dense table cells, where a filled tag would shout. */
export function ComposeStatusInline({
  lastDeployedAt,
  status,
}: {
  lastDeployedAt?: string | null;
  status: ComposeStatus;
}) {
  const state = composeState(status, lastDeployedAt);
  const meta = COMPOSE_STATE_META[state];
  return (
    <Tooltip title={meta.hint}>
      <Flex align="center" gap={8}>
        <Badge status={meta.badge} />
        <span>{meta.label}</span>
        {status.total > 0 && (
          <Text type="secondary">
            {status.running}/{status.total}
          </Text>
        )}
      </Flex>
    </Tooltip>
  );
}

export function ServiceStateTag({ service }: { service: ComposeService }) {
  const meta = SERVICE_STATE_META[service.state];
  return (
    <Tag color={meta.badge === 'default' ? undefined : meta.badge} variant="filled">
      {meta.label}
    </Tag>
  );
}

const HEALTH_META: Record<ServiceHealth, { badge: 'default' | 'error' | 'success' | 'warning'; label: string } | undefined> = {
  healthy: { badge: 'success', label: 'healthy' },
  unhealthy: { badge: 'error', label: 'unhealthy' },
  starting: { badge: 'warning', label: 'starting' },
  none: undefined,
};

export function HealthBadge({ health }: { health: ServiceHealth }) {
  const meta = HEALTH_META[health];
  if (!meta) return null;
  return (
    <Tooltip title={`Health check: ${meta.label}`}>
      <Badge status={meta.badge} />
    </Tooltip>
  );
}

/**
 * Page-level header. Every content page opens with exactly one of these so the
 * title, its context trail, and the screen's single primary action always sit
 * in the same place.
 */
export function PageHeader({
  breadcrumb,
  description,
  extra,
  tags,
  title,
  titleExtra,
}: {
  breadcrumb?: { href?: string; title: ReactNode }[];
  description?: ReactNode;
  extra?: ReactNode;
  tags?: ReactNode;
  title: ReactNode;
  titleExtra?: ReactNode;
}) {
  const { token } = theme.useToken();
  return (
    <div style={{ marginBottom: LAYOUT.gutter }}>
      {breadcrumb && breadcrumb.length > 0 && (
        <Breadcrumb items={breadcrumb} style={{ marginBottom: 12 }} />
      )}
      <Flex align="flex-start" gap={LAYOUT.gutterSmall} justify="space-between" wrap>
        <div style={{ minWidth: 0 }}>
          <Flex align="center" gap={12} wrap>
            <Title level={3} style={{ margin: 0 }}>
              {title}
            </Title>
            {tags}
          </Flex>
          {description && (
            <Text type="secondary" style={{ display: 'block', marginTop: 4, maxWidth: 720 }}>
              {description}
            </Text>
          )}
          {titleExtra && <div style={{ marginTop: 8 }}>{titleExtra}</div>}
        </div>
        {extra && (
          <Flex align="center" gap={8} wrap style={{ paddingTop: 2 }}>
            {extra}
          </Flex>
        )}
      </Flex>
      <div style={{ borderBottom: `1px solid ${token.colorBorderSecondary}`, marginTop: 16 }} />
    </div>
  );
}

/** A labelled, copyable mono value — deploy paths, project names, variable keys. */
export function CodeValue({
  actions,
  copyable,
  label,
  value,
}: {
  actions?: ReactNode;
  copyable?: boolean;
  label?: ReactNode;
  value: string;
}) {
  const { token } = theme.useToken();
  return (
    <Flex align="center" gap={8} justify="space-between" style={{ minWidth: 0 }}>
      <Typography.Text
        copyable={copyable ? { text: value, tooltips: ['Copy', 'Copied'] } : false}
        style={{
          background: token.colorFillQuaternary,
          borderRadius: token.borderRadiusSM,
          color: token.colorTextSecondary,
          fontFamily: CODE_FONT_STACK,
          fontSize: 12,
          maxWidth: '100%',
          overflow: 'hidden',
          padding: '2px 6px',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
        title={value}
      >
        {label ? <Text type="secondary">{label} </Text> : null}
        {value}
      </Typography.Text>
      {actions}
    </Flex>
  );
}

export function StatCard({
  hint,
  icon,
  title,
  value,
}: {
  hint?: ReactNode;
  icon?: ReactNode;
  title: ReactNode;
  value: ReactNode;
}) {
  const { token } = theme.useToken();
  return (
    <Card styles={{ body: { padding: 20 } }} variant="outlined">
      <Flex align="flex-start" gap={12} justify="space-between">
        <div style={{ minWidth: 0 }}>
          <Text type="secondary" style={{ fontSize: 13 }}>
            {title}
          </Text>
          <div style={{ fontSize: 24, fontWeight: 600, lineHeight: '32px', marginTop: 4 }}>{value}</div>
        </div>
        {icon && (
          <div
            style={{
              alignItems: 'center',
              background: token.colorFillQuaternary,
              borderRadius: token.borderRadius,
              color: token.colorTextSecondary,
              display: 'flex',
              flex: 'none',
              fontSize: 16,
              height: 32,
              justifyContent: 'center',
              width: 32,
            }}
          >
            {icon}
          </div>
        )}
      </Flex>
      {hint && (
        <Text type="secondary" style={{ display: 'block', fontSize: 12, marginTop: 8 }}>
          {hint}
        </Text>
      )}
    </Card>
  );
}

/** Empty states always offer the action that fills them. */
export function EmptyState({
  action,
  description,
  title,
}: {
  action?: ReactNode;
  description?: ReactNode;
  title: ReactNode;
}) {
  const { token } = theme.useToken();
  return (
    <Flex align="center" gap={4} style={{ padding: '40px 16px', textAlign: 'center' }} vertical>
      <div
        style={{
          alignItems: 'center',
          background: token.colorFillQuaternary,
          borderRadius: '50%',
          color: token.colorTextTertiary,
          display: 'flex',
          fontSize: 20,
          height: 48,
          justifyContent: 'center',
          marginBottom: 12,
          width: 48,
        }}
      >
        {action ? '＋' : '·'}
      </div>
      <Title level={5} style={{ margin: 0 }}>
        {title}
      </Title>
      {description && (
        <Text type="secondary" style={{ display: 'block', maxWidth: 420 }}>
          {description}
        </Text>
      )}
      {action && <div style={{ marginTop: 16 }}>{action}</div>}
    </Flex>
  );
}
