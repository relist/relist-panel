import Editor from '@monaco-editor/react';
import type { OnMount } from '@monaco-editor/react';
import { Alert, Button, Drawer, Empty, Flex, Select, Switch, Table, Tabs, Tag, Tree, Typography } from 'antd';
import type { DataNode } from 'antd/es/tree';
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { MONACO_THEME, ThemeModeContext, LAYOUT } from '../app/theme';
import { EditorPending, useMonaco } from '../components/MonacoGate';
import { MONACO_OPTIONS } from '../components/YamlEditor';
import { api } from '../data/api';
import type { Database, QueryCell, QueryResponse, QueryResult } from '../data/types';
import { formatBytes } from './databases-view';
import { cellText, pushHistory, readHistory, resultLabel, toCSV, utf16Index } from './query-view';
import type { HistoryEntry } from './query-view';

const { Text } = Typography;

const COLUMN_WIDTH = 180;
const MARKER_OWNER = 'relist-query';

function failure(reason: unknown): string {
  return reason instanceof Error ? reason.message : 'The query could not be sent';
}

function outcomeOf(response: QueryResponse): string {
  if (response.preview) return 'preview';
  if (response.cancelled) return 'cancelled';
  if (response.error) return 'error';
  return response.stopped ? 'stopped' : 'ok';
}

/** "Would delete 3,412 rows from orders" — one Preview statement's effect. */
function previewLine(result: QueryResult) {
  const verb = result.verb || 'change';
  const count = (result.affected ?? 0).toLocaleString('en-US');
  const rows = result.affected === 1 ? 'row' : 'rows';
  if (!result.target) return `Would ${verb} ${count} ${rows}`;
  return (
    <>
      Would {verb} {count} {rows} from <Text code>{result.target}</Text>
    </>
  );
}

function CellView({ cell }: { cell: QueryCell }) {
  if (cell === null) return <i style={{ opacity: 0.55 }}>NULL</i>;
  if (typeof cell === 'string') return <span title={cell.length > 40 ? cell : undefined}>{cell}</span>;
  const binary = cell.hex !== undefined;
  return (
    <span>
      {cellText(cell)}{' '}
      <Tag>{binary ? `binary, ${formatBytes(cell.bytes)}` : `cut, ${formatBytes(cell.bytes)}`}</Tag>
    </span>
  );
}

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  URL.revokeObjectURL(url);
}

function ResultView({ result, database }: { database: string; result: QueryResult }) {
  const columns = useMemo(
    () =>
      result.columns.map((column, i) => ({
        dataIndex: 'cells',
        ellipsis: true,
        key: `${i}`,
        render: (cells: QueryCell[]) => <CellView cell={cells[i]} />,
        title: (
          <span title={column.type}>
            {column.name} <Text type="secondary">{column.type}</Text>
          </span>
        ),
        width: COLUMN_WIDTH,
      })),
    [result.columns],
  );
  const data = useMemo(() => result.rows.map((cells, key) => ({ cells, key })), [result.rows]);
  if (result.columns.length === 0) {
    return (
      <Text>
        {result.affected === null ? result.command : `${result.affected} rows affected`}
        <Text type="secondary"> · {result.durationMs} ms</Text>
      </Text>
    );
  }
  return (
    <Flex gap={8} vertical>
      <Flex align="center" gap={8} justify="space-between" wrap>
        <Text type="secondary">
          {result.rows.length} rows · {result.durationMs} ms
          {result.truncated && (
            <Tag color="warning" style={{ marginInlineStart: 8 }}>
              First {result.rows.length.toLocaleString('en-US')} rows
            </Tag>
          )}
        </Text>
        <Button onClick={() => download(`${database}-result-${result.statementIndex + 1}.csv`, toCSV(result.columns, result.rows))} size="small">
          Export CSV
        </Button>
      </Flex>
      <Table
        columns={columns}
        dataSource={data}
        pagination={false}
        scroll={{ x: Math.max(columns.length * COLUMN_WIDTH, 600), y: 400 }}
        size="small"
        virtual
      />
    </Flex>
  );
}

/** The SQL console of a Postgres, MySQL or MariaDB Database (GUIDELINES §4.6a). */
export function DatabaseQueryTab({ db }: { db: Database }) {
  const mysql = db.engine !== 'postgres';
  const mode = useContext(ThemeModeContext);
  const monacoStatus = useMonaco();
  const running = db.status === 'running';

  const logical = useSWR(`/api/databases/${db.id}/logical-databases`, () => api.databaseLogicalDatabases(db.id), {
    isPaused: () => !running,
  });
  const options = useMemo(() => {
    const names = logical.data?.databases ?? [];
    return [...new Set([...(db.defaultDatabase ? [db.defaultDatabase] : []), ...names])];
  }, [db.defaultDatabase, logical.data]);
  const [chosen, setChosen] = useState<string>();
  const database = chosen && options.includes(chosen) ? chosen : (options[0] ?? '');

  const schema = useSWR(running && database ? [`/api/databases/${db.id}/query/schema`, database] : null, () =>
    api.databaseQuerySchema(db.id, database),
  );
  const treeData: DataNode[] = useMemo(
    () =>
      (schema.data ?? []).map((node) => ({
        children: node.tables.map((table) => ({
          isLeaf: true,
          key: `t:${node.name}\u0000${table}`,
          title: node.views?.includes(table) ? (
            <span>
              <span>{table}</span> <Tag bordered={false}>view</Tag>
            </span>
          ) : (
            table
          ),
        })),
        key: `s:${node.name}`,
        title: node.name,
      })),
    [schema.data],
  );

  const [sql, setSql] = useState('');
  const [writes, setWrites] = useState(false);
  const [busy, setBusy] = useState(false);
  const [response, setResponse] = useState<QueryResponse>();
  const [notice, setNotice] = useState<string>();
  const [active, setActive] = useState('0');
  const [historyOpen, setHistoryOpen] = useState(false);
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const abort = useRef<AbortController | null>(null);
  const editorRef = useRef<Parameters<OnMount>[0] | null>(null);
  const monacoRef = useRef<Parameters<OnMount>[1] | null>(null);

  const mark = useCallback((offset: number | null, message: string) => {
    const editor = editorRef.current;
    const monaco = monacoRef.current;
    const model = editor?.getModel();
    if (!editor || !monaco || !model) return;
    if (offset === null) {
      monaco.editor.setModelMarkers(model, MARKER_OWNER, []);
      return;
    }
    const start = model.getPositionAt(offset);
    const word = model.getWordAtPosition(start);
    const end = word ? { column: word.endColumn, lineNumber: start.lineNumber } : { column: start.column + 1, lineNumber: start.lineNumber };
    monaco.editor.setModelMarkers(model, MARKER_OWNER, [
      {
        endColumn: end.column,
        endLineNumber: end.lineNumber,
        message,
        severity: monaco.MarkerSeverity.Error,
        startColumn: start.column,
        startLineNumber: start.lineNumber,
      },
    ]);
  }, []);

  const run = useCallback(
    async (explicit?: string, preview = false) => {
      if (busy || !running) return;
      let text = explicit ?? sql;
      let offset = 0;
      if (explicit === undefined) {
        const editor = editorRef.current;
        const model = editor?.getModel();
        const selection = editor?.getSelection();
        if (editor && model && selection && !selection.isEmpty()) {
          text = model.getValueInRange(selection);
          offset = model.getOffsetAt(selection.getStartPosition());
        }
      }
      if (text.trim() === '') return;
      const controller = new AbortController();
      abort.current = controller;
      setBusy(true);
      setNotice(undefined);
      setResponse(undefined);
      mark(null, '');
      const entry = { at: Date.now(), database, sql: text, writes };
      try {
        const answer = await api.runDatabaseQuery(db.id, { database, preview, sql: text, writes }, controller.signal);
        setResponse(answer);
        setActive('0');
        pushHistory(db.id, { ...entry, outcome: outcomeOf(answer) });
        if (answer.error && !answer.cancelled) {
          const at = answer.error.position ?? answer.error.statementStart;
          mark(offset + utf16Index(text, at), answer.error.message);
        }
      } catch (reason: unknown) {
        if (controller.signal.aborted) {
          setNotice('Cancelled.');
          pushHistory(db.id, { ...entry, outcome: 'cancelled' });
        } else {
          setNotice(failure(reason));
        }
      } finally {
        abort.current = null;
        setBusy(false);
      }
    },
    [busy, database, db.id, mark, running, sql, writes],
  );

  // The editor's Ctrl/Cmd+Enter command is registered once; it calls the latest run.
  const runRef = useRef(run);
  useEffect(() => {
    runRef.current = run;
  }, [run]);
  useEffect(() => () => abort.current?.abort(), []);

  const onMount: OnMount = (editor, monaco) => {
    editorRef.current = editor;
    monacoRef.current = monaco;
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => void runRef.current());
  };

  // A click only fills the editor: reading a view runs the functions it
  // calls, so nothing runs until Run is pressed.
  const pickTable = (schemaName: string, table: string) => {
    const query = mysql
      ? `SELECT * FROM \`${table.replaceAll('`', '``')}\` LIMIT 100`
      : `SELECT * FROM "${schemaName.replaceAll('"', '""')}"."${table.replaceAll('"', '""')}" LIMIT 100`;
    setSql(query);
  };

  return (
    <Flex gap={16} vertical>
      {!running && (
        <Alert
          showIcon
          title={`The Database is not running (${db.status}). Start it to run queries.`}
          type="warning"
        />
      )}
      <Flex align="center" gap={16} wrap>
        <Select
          aria-label="Database"
          disabled={!running}
          onChange={setChosen}
          options={options.map((name) => ({ label: name, value: name }))}
          style={{ minWidth: 200 }}
          value={database || undefined}
        />
        <Flex align="center" gap={8}>
          <Switch aria-label="Allow writes" checked={writes} onChange={setWrites} />
          <Text>Allow writes</Text>
          {writes && <Tag color="red">Writes allowed</Tag>}
        </Flex>
        <Flex gap={8} style={{ marginInlineStart: 'auto' }}>
          <Button
            onClick={() => {
              setHistory(readHistory(db.id));
              setHistoryOpen(true);
            }}
          >
            History
          </Button>
          {busy && <Button onClick={() => abort.current?.abort()}>Cancel</Button>}
          {writes && (
            <Button disabled={!running || busy} onClick={() => void run(undefined, true)}>
              Preview
            </Button>
          )}
          <Button disabled={!running || busy} onClick={() => void run()} type="primary">
            Run
          </Button>
        </Flex>
      </Flex>

      <Flex gap={16} wrap>
        <div style={{ flex: '1 1 220px', maxWidth: 320, minWidth: 0 }}>
          {treeData.length === 0 ? (
            <Empty description={schema.isLoading ? 'Loading tables…' : 'No tables'} image={Empty.PRESENTED_IMAGE_SIMPLE} />
          ) : (
            <Tree
              blockNode
              defaultExpandAll
              key={`${database}:${treeData.length}`}
              onSelect={(_keys, info) => {
                const key = String(info.node.key);
                if (!key.startsWith('t:')) return;
                const [schemaName, table] = key.slice(2).split('\u0000');
                pickTable(schemaName, table);
              }}
              selectable
              treeData={treeData}
            />
          )}
        </div>
        <Flex gap={16} style={{ flex: '4 1 480px', minWidth: 0 }} vertical>
          <div style={{ border: '1px solid var(--ant-color-border-secondary, #f0f0f0)', height: LAYOUT.queryEditorHeight }}>
            {monacoStatus === 'ready' ? (
              <Editor
                height="100%"
                language={mysql ? 'mysql' : 'pgsql'}
                loading={<span>Loading editor…</span>}
                onChange={(next) => setSql(next ?? '')}
                onMount={onMount}
                options={MONACO_OPTIONS}
                path={`query:${db.id}`}
                theme={MONACO_THEME[mode]}
                value={sql}
              />
            ) : (
              <EditorPending height="100%" status={monacoStatus} />
            )}
          </div>
          <Text type="secondary">Runs the selection, or the whole editor. Ctrl or Cmd + Enter runs.</Text>
          {notice && <Alert showIcon title={notice} type={notice === 'Cancelled.' ? 'info' : 'error'} />}
          {response?.stopped && <Alert showIcon title={response.stopped} type="warning" />}
          {response?.error && (
            <Alert
              description={
                response.results.length > 0 || response.error.statementIndex > 0
                  ? `Statement ${response.error.statementIndex + 1}`
                  : undefined
              }
              showIcon
              title={response.error.message}
              type="error"
            />
          )}
          {response?.preview && !response.error && (
            <Alert
              description={
                <Flex gap={4} vertical>
                  {response.results.map((result) => (
                    <span key={result.statementIndex}>{previewLine(result)}</span>
                  ))}
                  <Text type="secondary">
                    Preview rolls everything back. It cannot undo sequence values, or anything a trigger or function does
                    outside this database&apos;s tables.
                  </Text>
                </Flex>
              }
              showIcon
              title="Preview: nothing was changed."
              type="info"
            />
          )}
          {response && !response.preview && response.results.length > 0 && (
            <Tabs
              activeKey={active}
              items={response.results.map((result, i) => ({
                children: <ResultView database={database} result={result} />,
                key: `${i}`,
                label: resultLabel(result, i),
              }))}
              onChange={setActive}
              size="small"
            />
          )}
        </Flex>
      </Flex>

      <Drawer onClose={() => setHistoryOpen(false)} open={historyOpen} size="large" title="History">
        {history.length === 0 ? (
          <Empty description="No runs yet in this browser" />
        ) : (
          <Flex gap={12} vertical>
            {history.map((entry) => (
              <Button
                block
                key={`${entry.at}`}
                onClick={() => {
                  setSql(entry.sql);
                  setChosen(entry.database);
                  setHistoryOpen(false);
                }}
                style={{ height: 'auto', textAlign: 'start', whiteSpace: 'normal' }}
              >
                <Flex gap={4} vertical>
                  <Text code style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                    {entry.sql.length > 300 ? `${entry.sql.slice(0, 300)}…` : entry.sql}
                  </Text>
                  <Text type="secondary">
                    {new Date(entry.at).toLocaleString()} · {entry.database} · {entry.outcome}
                    {entry.writes ? ' · writes allowed' : ''}
                  </Text>
                </Flex>
              </Button>
            ))}
          </Flex>
        )}
      </Drawer>
    </Flex>
  );
}
