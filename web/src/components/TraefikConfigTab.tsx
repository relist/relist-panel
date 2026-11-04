import { HistoryOutlined } from '@ant-design/icons';
import { Alert, App, Button, Flex, Form, Input, Popconfirm, Segmented, Select, Switch, Table, Tag, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useRef, useState } from 'react';
import useSWR from 'swr';

import { ApiError, api } from '../data/api';
import type { ConfigProblem, TraefikConfig, TraefikForm, TraefikHistoryEntry, TraefikLogLevel } from '../data/types';
import { ipsFromText, isDirty } from '../pages/traefik-config-view';
import { YamlEditor } from './YamlEditor';

const { Text } = Typography;

/** The sentence Review & Apply and Restore both show: Traefik restarts either way. */
export const RESTART_DROP = 'Every Domain and this panel drop for a few seconds while Traefik restarts';

const PREVIEW_DELAY_MS = 400;

const LOG_LEVELS: TraefikLogLevel[] = ['ERROR', 'WARN', 'INFO', 'DEBUG'];

const EMPTY_FORM: TraefikForm = { accessLog: false, acmeEmail: '', logLevel: 'INFO', trustCloudflare: false, trustedIps: [] };

type View = 'settings' | 'advanced' | 'history';
type FileView = 'override' | 'base' | 'effective';

function problemText(problem: ConfigProblem): string {
  return problem.line > 0 ? `Line ${problem.line}: ${problem.message}` : problem.message;
}

/**
 * The Configuration tab of the Traefik page (ADR-0030). The draft is the
 * override's text: a Settings edit goes through `preview` and takes the
 * override it returns; an Advanced edit is the draft, and `preview` refreshes
 * the errors and the form from it.
 */
export function TraefikConfigTab({
  config,
  onReviewApply,
  onStarted,
}: {
  config: TraefikConfig;
  /** Review & Apply: the draft and the file it generates. */
  onReviewApply: (draft: string, effective: string) => void;
  /** A Restore started a Traefik apply with this id. */
  onStarted: (id: string) => void;
}) {
  const { message } = App.useApp();
  const [view, setView] = useState<View>('settings');
  const [fileView, setFileView] = useState<FileView>('override');
  const [draft, setDraft] = useState(config.override);
  const [form, setForm] = useState<TraefikForm>(config.form ?? EMPTY_FORM);
  const [parsed, setParsed] = useState(config.form !== null);
  const [ipsText, setIpsText] = useState((config.form?.trustedIps ?? []).join('\n'));
  const [effective, setEffective] = useState(config.effective);
  const [errors, setErrors] = useState<ConfigProblem[]>([]);
  const [warnings, setWarnings] = useState<ConfigProblem[]>(config.warnings);
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState<string>();
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  // Only the latest preview's answer is used; an older one landing late is dropped.
  const sequence = useRef(0);

  const reset = (c: TraefikConfig) => {
    clearTimeout(timer.current);
    sequence.current += 1;
    setDraft(c.override);
    setForm(c.form ?? EMPTY_FORM);
    setParsed(c.form !== null);
    setIpsText((c.form?.trustedIps ?? []).join('\n'));
    setEffective(c.effective);
    setErrors([]);
    setWarnings(c.warnings);
    setPreviewing(false);
    setPreviewError(undefined);
  };

  // A new revision (an apply finished, or someone else changed the file) replaces the draft.
  useEffect(() => {
    reset(config);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only a new revision replaces the draft
  }, [config.revision]);

  useEffect(() => () => clearTimeout(timer.current), []);

  const schedulePreview = (override: string, edited?: TraefikForm) => {
    clearTimeout(timer.current);
    const id = ++sequence.current;
    setPreviewing(true);
    timer.current = setTimeout(() => {
      api
        .previewTraefikConfig(edited ? { form: edited, override } : { override })
        .then((resp) => {
          if (id !== sequence.current) return;
          if (edited) setDraft(resp.override);
          if (resp.form) {
            setForm(resp.form);
            // A form edit keeps the text as typed; an Advanced edit rewrites it.
            if (!edited) setIpsText(resp.form.trustedIps.join('\n'));
          }
          setParsed(resp.form !== null);
          setEffective(resp.effective);
          setErrors(resp.errors);
          setWarnings(resp.warnings);
          setPreviewError(undefined);
        })
        .catch((reason: unknown) => {
          if (id !== sequence.current) return;
          setPreviewError(reason instanceof ApiError ? reason.message : 'Could not check the draft');
        })
        .finally(() => {
          if (id === sequence.current) setPreviewing(false);
        });
    }, PREVIEW_DELAY_MS);
  };

  const editForm = (patch: Partial<TraefikForm>) => {
    const next = { ...form, ...patch };
    setForm(next);
    schedulePreview(draft, next);
  };

  const editYaml = (text: string) => {
    setDraft(text);
    schedulePreview(text);
  };

  const dirty = isDirty(draft, config.override);
  const canReview = dirty && !previewing && errors.length === 0 && previewError === undefined;
  const bootstrapWarning = config.lastApply?.bootstrapWarning;

  return (
    <Flex gap={16} vertical>
      {bootstrapWarning && <Alert showIcon title={bootstrapWarning} type="warning" />}
      {warnings.map((warning) => (
        <Alert key={`${warning.key}-${warning.message}`} showIcon title={warning.message} type="warning" />
      ))}
      {previewError && <Alert showIcon title={previewError} type="error" />}

      <Segmented<View>
        onChange={setView}
        options={[
          { label: 'Settings', value: 'settings' },
          { label: 'Advanced', value: 'advanced' },
          { label: 'History', value: 'history' },
        ]}
        style={{ alignSelf: 'flex-start' }}
        value={view}
      />

      {view === 'settings' && (
        <SettingsForm
          disabled={!parsed}
          form={form}
          ipsText={ipsText}
          onChange={editForm}
          onIpsText={(text) => {
            setIpsText(text);
            editForm({ trustedIps: ipsFromText(text) });
          }}
        />
      )}

      {view === 'advanced' && (
        <Flex gap={8} vertical>
          <Segmented<FileView>
            onChange={setFileView}
            options={[
              { label: 'Override', value: 'override' },
              { label: 'Base', value: 'base' },
              { label: 'Effective', value: 'effective' },
            ]}
            size="small"
            style={{ alignSelf: 'flex-start' }}
            value={fileView}
          />
          {fileView === 'override' && (
            <YamlEditor autoHeight={{ max: '70vh', min: 240 }} onChange={(text) => editYaml(text ?? '')} path="traefik.override.yml" value={draft} />
          )}
          {fileView === 'base' && (
            <YamlEditor autoHeight={{ max: '70vh', min: 240 }} path="traefik.base.yml" readOnly value={config.base} />
          )}
          {fileView === 'effective' && (
            <YamlEditor autoHeight={{ max: '70vh', min: 240 }} path="traefik.yml" readOnly value={effective} />
          )}
          {errors.map((problem) => (
            <Alert key={`${problem.line}-${problem.key}-${problem.message}`} showIcon title={problemText(problem)} type="error" />
          ))}
        </Flex>
      )}

      {view === 'history' && (
        <HistoryTable
          onRestore={(name) =>
            api
              .restoreTraefikConfig(name)
              .then(({ id }) => onStarted(id))
              .catch((reason: unknown) => {
                void message.error(reason instanceof ApiError ? reason.message : 'Could not restore');
              })
          }
        />
      )}

      <Flex align="center" gap={8} justify="flex-end" wrap>
        {dirty && <Tag color="warning">Unsaved changes</Tag>}
        <Button disabled={!dirty} onClick={() => reset(config)}>
          Discard
        </Button>
        <Button disabled={!canReview} onClick={() => onReviewApply(draft, effective)} type="primary">
          Review &amp; Apply
        </Button>
      </Flex>
    </Flex>
  );
}

function SettingsForm({
  disabled,
  form,
  ipsText,
  onChange,
  onIpsText,
}: {
  disabled: boolean;
  form: TraefikForm;
  ipsText: string;
  onChange: (patch: Partial<TraefikForm>) => void;
  onIpsText: (text: string) => void;
}) {
  return (
    <Flex gap={8} vertical>
      {disabled && <Alert showIcon title="Fix the YAML in Advanced first" type="warning" />}
      <Form disabled={disabled} layout="vertical" style={{ maxWidth: 560 }}>
        <Form.Item extra="Let's Encrypt sends certificate expiry notices here. May be empty." htmlFor="traefik-acme-email" label="ACME email">
          <Input
            aria-label="ACME email"
            id="traefik-acme-email"
            onChange={(e) => onChange({ acmeEmail: e.target.value })}
            type="email"
            value={form.acmeEmail}
          />
        </Form.Item>
        <Form.Item
          extra="One IP or CIDR per line. Traefik believes X-Forwarded-For only from these addresses."
          htmlFor="traefik-trusted-ips"
          label="Trusted proxies"
        >
          <Input.TextArea
            aria-label="Trusted proxies"
            autoSize={{ maxRows: 12, minRows: 3 }}
            id="traefik-trusted-ips"
            onChange={(e) => onIpsText(e.target.value)}
            value={ipsText}
          />
        </Form.Item>
        <Form.Item extra="Adds Cloudflare's published IP ranges to the trusted proxies." label="Trust Cloudflare">
          <Switch aria-label="Trust Cloudflare" checked={form.trustCloudflare} onChange={(on) => onChange({ trustCloudflare: on })} />
        </Form.Item>
        <Form.Item htmlFor="traefik-log-level" label="Log level">
          <Select<TraefikLogLevel>
            aria-label="Log level"
            id="traefik-log-level"
            onChange={(logLevel) => onChange({ logLevel })}
            options={LOG_LEVELS.map((level) => ({ label: level, value: level }))}
            style={{ width: 160 }}
            value={form.logLevel}
          />
        </Form.Item>
        <Form.Item extra="Writes every request to the Access log files, rotated daily." label="Access log">
          <Switch aria-label="Access log" checked={form.accessLog} onChange={(on) => onChange({ accessLog: on })} />
        </Form.Item>
      </Form>
    </Flex>
  );
}

function HistoryTable({ onRestore }: { onRestore: (name: string) => Promise<void> }) {
  const history = useSWR('/api/traefik/config/history', () => api.traefikConfigHistory());
  const columns: TableColumnsType<TraefikHistoryEntry> = [
    { dataIndex: 'at', key: 'at', render: (at: string) => new Date(at).toLocaleString(), title: 'Time' },
    { dataIndex: 'kind', key: 'kind', title: 'Kind' },
    { dataIndex: 'result', key: 'result', title: 'Result' },
    {
      align: 'right',
      key: 'actions',
      render: (_, entry) => (
        <Popconfirm description={`${RESTART_DROP}.`} okText="Restore" onConfirm={() => onRestore(entry.name)} title={`Restore ${entry.name}?`}>
          <Button aria-label={`Restore ${entry.name}`} icon={<HistoryOutlined />} type="text" />
        </Popconfirm>
      ),
      title: '',
    },
  ];

  return (
    <>
      {history.error && (
        <Alert
          showIcon
          title={history.error instanceof Error ? history.error.message : 'Could not list backups'}
          type="error"
        />
      )}
      <Table<TraefikHistoryEntry>
        columns={columns}
        dataSource={history.data ?? []}
        loading={history.isLoading}
        locale={{ emptyText: history.isLoading ? ' ' : <Text type="secondary">No backups yet.</Text> }}
        pagination={false}
        rowKey="name"
        scroll={{ x: 'max-content' }}
        size="small"
      />
    </>
  );
}
