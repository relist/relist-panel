import { FilterOutlined } from '@ant-design/icons';
import { Alert, Button, DatePicker, Flex, Input, Table, Tag, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import { useCallback, useEffect, useRef, useState } from 'react';

import type { AuditEvent, AuditPage, AuditQuery } from '../data/api';
import { RelativeTime } from './primitives';

const { Text } = Typography;

type Filters = Omit<AuditQuery, 'cursor'>;

/** A detail object as short `key: value` text; arrays and objects as JSON. */
function detailText(detail: AuditEvent['detail']): string {
  if (!detail) return '';
  return Object.entries(detail)
    .map(([key, value]) => `${key}: ${typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean' ? value : JSON.stringify(value)}`)
    .join(', ');
}

/** The filters without empty fields, so the request carries only what was set. */
function compact(filters: Filters): Filters {
  const out: Filters = {};
  if (filters.action?.trim()) out.action = filters.action.trim();
  if (filters.actor?.trim()) out.actor = filters.actor.trim();
  if (filters.from) out.from = filters.from;
  if (filters.to) out.to = filters.to;
  return out;
}

/**
 * The Audit log as a table (spec: UI → Audit log, Project → Activity):
 * filters for actor, action prefix and day range, newest first, and Load
 * more for the next cursor page. `load` fetches one page.
 */
export function AuditTable({ load }: { load: (query: AuditQuery) => Promise<AuditPage> }) {
  const [draft, setDraft] = useState<{ actor: string; action: string }>({ actor: '', action: '' });
  const [range, setRange] = useState<[Dayjs, Dayjs] | null>(null);
  const [filters, setFilters] = useState<Filters>({});
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [nextCursor, setNextCursor] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  // A response for older filters or an older page is dropped.
  const generation = useRef(0);

  const fetchPage = useCallback(
    async (query: Filters, cursor?: number) => {
      const current = ++generation.current;
      setLoading(true);
      setError(undefined);
      try {
        const page = await load(cursor ? { ...query, cursor } : query);
        if (current !== generation.current) return;
        setItems((previous) => (cursor ? [...previous, ...page.items] : page.items));
        setNextCursor(page.nextCursor);
      } catch (reason) {
        if (current !== generation.current) return;
        setError(reason instanceof Error ? reason.message : 'Could not load the audit log');
      } finally {
        if (current === generation.current) setLoading(false);
      }
    },
    [load],
  );

  useEffect(() => {
    void fetchPage(filters);
  }, [fetchPage, filters]);

  const apply = (nextRange = range) =>
    setFilters(
      compact({
        ...draft,
        from: nextRange ? nextRange[0].startOf('day').valueOf() : undefined,
        to: nextRange ? nextRange[1].endOf('day').valueOf() : undefined,
      }),
    );

  const columns: TableColumnsType<AuditEvent> = [
    {
      key: 'at',
      render: (_, row) => <RelativeTime value={dayjs(row.at).toISOString()} />,
      title: 'Time',
      width: 140,
    },
    { dataIndex: 'actor', ellipsis: true, key: 'actor', title: 'Actor', width: 200 },
    {
      dataIndex: 'action',
      key: 'action',
      render: (action: string, row) => (
        <Flex gap={4} wrap>
          <Tag>{action}</Tag>
          {row.outcome === 'denied' && <Tag color="red">denied</Tag>}
        </Flex>
      ),
      title: 'Action',
      width: 240,
    },
    { dataIndex: 'target', ellipsis: true, key: 'target', title: 'Target' },
    {
      key: 'detail',
      render: (_, row) => {
        const text = detailText(row.detail);
        return text ? <Text ellipsis={{ tooltip: text }} type="secondary">{text}</Text> : null;
      },
      title: 'Detail',
      width: 260,
    },
    { dataIndex: 'ip', key: 'ip', responsive: ['lg'], title: 'IP', width: 140 },
  ];

  return (
    <Flex gap={16} vertical>
      <Flex align="end" gap={12} wrap>
        <Flex gap={4} vertical>
          <label htmlFor="audit-actor">Actor</label>
          <Input
            allowClear
            id="audit-actor"
            onChange={(e) => setDraft({ ...draft, actor: e.target.value })}
            onPressEnter={() => apply()}
            placeholder="Email or cli"
            style={{ width: 220 }}
            value={draft.actor}
          />
        </Flex>
        <Flex gap={4} vertical>
          <label htmlFor="audit-action">Action</label>
          <Input
            allowClear
            id="audit-action"
            onChange={(e) => setDraft({ ...draft, action: e.target.value })}
            onPressEnter={() => apply()}
            placeholder="compose."
            style={{ width: 200 }}
            value={draft.action}
          />
        </Flex>
        <Flex gap={4} vertical>
          <label htmlFor="audit-from">Date</label>
          <DatePicker.RangePicker
            id={{ start: 'audit-from', end: 'audit-to' }}
            onChange={(dates) => {
              const next = dates?.[0] && dates[1] ? ([dates[0], dates[1]] as [Dayjs, Dayjs]) : null;
              setRange(next);
              apply(next);
            }}
            placeholder={['From date', 'To date']}
            value={range}
          />
        </Flex>
        <Button icon={<FilterOutlined />} onClick={() => apply()}>
          Filter
        </Button>
      </Flex>
      {error && (
        <Alert
          action={<Button onClick={() => void fetchPage(filters)}>Retry</Button>}
          description={error}
          showIcon
          title="Could not load the audit log"
          type="error"
        />
      )}
      <Table<AuditEvent>
        columns={columns}
        dataSource={items}
        loading={loading}
        pagination={false}
        rowKey="id"
        scroll={{ x: 'max-content' }}
        size="small"
      />
      {nextCursor !== null && (
        <Flex justify="center">
          <Button loading={loading} onClick={() => void fetchPage(filters, nextCursor)}>
            Load more
          </Button>
        </Flex>
      )}
    </Flex>
  );
}
