import { Alert, Button, Descriptions, Flex, Input, Modal, Select, Switch, Table, Tag, Typography } from 'antd';
import { useRef, useState } from 'react';

import { api } from '../data/api';
import type { Database, RedisKeyInfo, RedisKeyValue, RedisResponse } from '../data/types';
import { formatBytes } from './databases-view';
import { formatReply, pushRedisHistory, readRedisHistory, ttlText } from './redis-view';

const { Text } = Typography;

const DBS = Array.from({ length: 16 }, (_, n) => ({ label: `db ${n}`, value: n }));

interface LogEntry {
  command: string;
  error?: string;
  id: number;
  refused?: string;
  text?: string;
  tone?: 'error';
}

function failure(reason: unknown): string {
  return reason instanceof Error ? reason.message : 'The command could not be sent';
}

function entryFor(id: number, command: string, response: RedisResponse): LogEntry {
  if (response.cancelled) return { command, error: response.error ?? 'Cancelled.', id };
  if (response.error) return { command, error: response.error, id };
  if (response.refused) return { command, id, refused: response.refused };
  const reply = response.reply;
  return { command, id, text: reply ? formatReply(reply) : '', tone: reply?.type === 'error' ? 'error' : undefined };
}

/** The console and key browser of a Redis Database (GUIDELINES §4.6b). */
export function DatabaseRedisTab({ db }: { db: Database }) {
  const running = db.status === 'running';
  // A Read grantee (ADR-0045): the API refuses writes, so the switch goes.
  const readOnly = db.access?.level === 'read';
  const [dbNumber, setDbNumber] = useState(0);
  const [writes, setWrites] = useState(false);
  const [line, setLine] = useState('');
  const [log, setLog] = useState<LogEntry[]>([]);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState<string>();
  const [history, setHistory] = useState(() => readRedisHistory(db.id));
  const recall = useRef(-1);
  const nextId = useRef(1);

  const send = async (command: string, confirmDangerous: boolean) => {
    setBusy(true);
    const id = nextId.current++;
    try {
      const response = await api.runRedisCommand(db.id, { command, confirmDangerous, db: dbNumber, writes });
      if (response.needsConfirm) {
        setConfirming(command);
        return;
      }
      setLog((entries) => [entryFor(id, command, response), ...entries]);
    } catch (reason: unknown) {
      setLog((entries) => [{ command, error: failure(reason), id }, ...entries]);
    } finally {
      setBusy(false);
    }
  };

  const submit = () => {
    const command = line.trim();
    if (command === '' || busy || !running) return;
    pushRedisHistory(db.id, command);
    setHistory(readRedisHistory(db.id));
    recall.current = -1;
    setLine('');
    void send(command, false);
  };

  const onKey = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowUp' && history.length > 0) {
      event.preventDefault();
      recall.current = Math.min(recall.current + 1, history.length - 1);
      setLine(history[recall.current]);
    } else if (event.key === 'ArrowDown' && recall.current >= 0) {
      event.preventDefault();
      recall.current -= 1;
      setLine(recall.current >= 0 ? history[recall.current] : '');
    }
  };

  // Key browser.
  const [pattern, setPattern] = useState('*');
  const [keys, setKeys] = useState<RedisKeyInfo[]>([]);
  const [cursor, setCursor] = useState('0');
  const [scanned, setScanned] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanError, setScanError] = useState<string>();
  const [value, setValue] = useState<RedisKeyValue>();
  const [valueError, setValueError] = useState<string>();

  const scan = async (from: string) => {
    setScanning(true);
    setScanError(undefined);
    try {
      const page = await api.redisKeys(db.id, dbNumber, pattern.trim() || '*', from);
      setKeys((current) => (from === '0' ? page.keys : [...current, ...page.keys]));
      setCursor(page.cursor);
      setScanned(true);
    } catch (reason: unknown) {
      setScanError(failure(reason));
    } finally {
      setScanning(false);
    }
  };

  const open = async (key: string) => {
    setValueError(undefined);
    try {
      setValue(await api.redisKey(db.id, dbNumber, key));
    } catch (reason: unknown) {
      setValue(undefined);
      setValueError(failure(reason));
    }
  };

  const command = confirming?.trim().split(/\s+/)[0]?.toUpperCase() ?? '';
  return (
    <Flex gap={16} vertical>
      {!running && (
        <Alert showIcon title={`The Database is not running (${db.status}). Start it to use the console.`} type="warning" />
      )}
      <Flex align="center" gap={16} wrap>
        <Select
          aria-label="Database number"
          disabled={!running}
          onChange={(n) => {
            setDbNumber(n);
            setKeys([]);
            setCursor('0');
            setScanned(false);
            setValue(undefined);
          }}
          options={DBS}
          style={{ minWidth: 120 }}
          value={dbNumber}
        />
        {!readOnly && (
          <Flex align="center" gap={8}>
            <Switch aria-label="Allow writes" checked={writes} onChange={setWrites} />
            <Text>Allow writes</Text>
            {writes && <Tag color="red">Writes allowed</Tag>}
          </Flex>
        )}
      </Flex>

      <Flex gap={16} wrap>
        <Flex gap={12} style={{ flex: '3 1 420px', minWidth: 0 }} vertical>
          <Input
            aria-label="Command"
            disabled={!running}
            onChange={(event) => {
              recall.current = -1;
              setLine(event.target.value);
            }}
            onKeyDown={onKey}
            onPressEnter={submit}
            placeholder='GET key, HGETALL key, SET key "a value"'
            style={{ fontFamily: 'var(--ant-font-family-code, monospace)' }}
            value={line}
          />
          <Text type="secondary">Enter runs. Up and Down recall earlier lines. Quotes work as in redis-cli.</Text>
          {log.length === 0 ? (
            <Text type="secondary">Nothing run yet.</Text>
          ) : (
            <Flex gap={12} vertical>
              {log.map((entry) => (
                <div key={entry.id}>
                  <Text code>{entry.command}</Text>
                  {entry.refused && (
                    <Alert showIcon style={{ marginTop: 4 }} title={entry.refused} type="warning" />
                  )}
                  {entry.error && <Alert showIcon style={{ marginTop: 4 }} title={entry.error} type="error" />}
                  {entry.text !== undefined && (
                    <pre
                      style={{
                        fontFamily: 'var(--ant-font-family-code, monospace)',
                        margin: '4px 0 0',
                        overflowWrap: 'anywhere',
                        whiteSpace: 'pre-wrap',
                      }}
                    >
                      {entry.text}
                    </pre>
                  )}
                </div>
              ))}
            </Flex>
          )}
        </Flex>

        <Flex gap={12} style={{ flex: '2 1 360px', minWidth: 0 }} vertical>
          <Flex gap={8}>
            <Input
              aria-label="Key pattern"
              disabled={!running}
              onChange={(event) => setPattern(event.target.value)}
              onPressEnter={() => void scan('0')}
              placeholder="*"
              value={pattern}
            />
            <Button disabled={!running} loading={scanning && cursor === '0'} onClick={() => void scan('0')}>
              Scan
            </Button>
          </Flex>
          {scanError && <Alert showIcon title={scanError} type="error" />}
          {scanned && (
            <Table
              columns={[
                { dataIndex: 'key', ellipsis: true, key: 'key', title: 'Key' },
                { dataIndex: 'type', key: 'type', render: (type: string) => <Tag>{type}</Tag>, title: 'Type', width: 90 },
                { dataIndex: 'ttlMs', key: 'ttl', render: (ms: number) => ttlText(ms), title: 'TTL', width: 110 },
              ]}
              dataSource={keys}
              locale={{ emptyText: 'No keys match' }}
              onRow={(row) => ({ onClick: () => void open(row.key), style: { cursor: 'pointer' } })}
              pagination={false}
              rowKey="key"
              size="small"
            />
          )}
          {scanned && cursor !== '0' && (
            <Button loading={scanning} onClick={() => void scan(cursor)}>
              Load more
            </Button>
          )}
          {valueError && <Alert showIcon title={valueError} type="error" />}
          {value && <KeyValueView value={value} />}
        </Flex>
      </Flex>

      <Modal
        cancelText="Cancel"
        centered
        destroyOnHidden
        okButtonProps={{ danger: true }}
        okText="Run"
        onCancel={() => setConfirming(undefined)}
        onOk={() => {
          const pending = confirming;
          setConfirming(undefined);
          if (pending) void send(pending, true);
        }}
        open={confirming !== undefined}
        title={`Run ${command}?`}
      >
        <Text>{command} can't be undone. Run it?</Text>
      </Modal>
    </Flex>
  );
}

function KeyValueView({ value }: { value: RedisKeyValue }) {
  const hash = value.type === 'hash';
  const zset = value.type === 'zset';
  const cell = (text: string, bytes?: number) => (
    <span style={{ overflowWrap: 'anywhere' }}>
      {text}
      {bytes ? <Tag style={{ marginInlineStart: 8 }}>cut, {formatBytes(bytes)}</Tag> : null}
    </span>
  );
  const columns = hash
    ? [
        { dataIndex: 'field', key: 'field', title: 'Field' },
        { dataIndex: 'value', key: 'value', render: (_: unknown, item: RedisKeyValue['items'][number]) => cell(item.value, item.bytes), title: 'Value' },
      ]
    : [
        { dataIndex: 'value', key: 'value', render: (_: unknown, item: RedisKeyValue['items'][number]) => cell(item.value, item.bytes), title: zset ? 'Member' : 'Value' },
        ...(zset ? [{ dataIndex: 'score', key: 'score', title: 'Score', width: 120 }] : []),
      ];
  return (
    <Flex gap={8} vertical>
      <Descriptions
        bordered
        column={1}
        items={[
          { children: <Text code>{value.key}</Text>, key: 'key', label: 'Key' },
          { children: <Tag>{value.type}</Tag>, key: 'type', label: 'Type' },
          { children: ttlText(value.ttlMs), key: 'ttl', label: 'TTL' },
          ...(value.type === 'string' || value.unsupported ? [] : [{ children: value.total.toLocaleString('en-US'), key: 'n', label: 'Elements' }]),
        ]}
        size="small"
      />
      {value.unsupported ? (
        <Alert showIcon title={`Values of type ${value.type} are not shown here. Use the console.`} type="info" />
      ) : (
        <Table
          columns={columns}
          dataSource={value.items.map((item, i) => ({ ...item, key: i }))}
          pagination={false}
          size="small"
        />
      )}
      {value.more && (
        <Text type="secondary">Showing the first 1,000 of {value.total.toLocaleString('en-US')}.</Text>
      )}
    </Flex>
  );
}
