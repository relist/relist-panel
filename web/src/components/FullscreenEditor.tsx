import {
  CheckCircleOutlined,
  CloseOutlined,
  CopyOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  FileTextOutlined,
  SaveOutlined,
  UndoOutlined,
} from '@ant-design/icons';
import { Alert, App, Button, Flex, Input, Modal, Tag, Tooltip, Typography, theme } from 'antd';
import { useMemo, useRef, useState } from 'react';
import type { editor as MonacoEditorApi } from 'monaco-editor';
import { useNavigate } from 'react-router';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { paths } from '../app/paths';
import { ComposeStatusTag, EmptyState } from './primitives';
import { YamlEditor } from './YamlEditor';
import { isSecret, maskedValue, railVariables, type RailVariable } from './variables-view';
import type { Compose, ComposeVariable, Environment, Project } from '../data/types';

const { Text } = Typography;

const NO_VARIABLES: readonly ComposeVariable[] = [];

/** The one way a Compose refers to an Environment Variable. Values are injected at deploy time. */
const reference = (key: string) => `\${${key}}`;

/**
 * One Environment Variable in the editor's rail. The whole row inserts `${KEY}`
 * at the cursor; the copy button puts the same reference on the clipboard,
 * because that is what the operator is pasting into the file.
 *
 * Two text lines, one action anchor. The key line ends in the key's metadata
 * (`secret` / `plain`), the value line ends in the value's own actions (reveal,
 * copy), and `Insert` sits alone at the end of the row, centred against both —
 * the "already in the file" state belongs to that action, not to the tag.
 */
function VariableRow({
  draft,
  onInsert,
  variable,
}: {
  draft: string;
  onInsert: (key: string) => void;
  variable: RailVariable;
}) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const [revealed, setRevealed] = useState(false);
  // The stored flag, the same source the Variables tab reads — the marking
  // is the user's answer, never a client-side guess.
  const secret = isSecret(variable);
  const masked = maskedValue(variable.value);
  const alreadyReferenced = draft.includes(reference(variable.key));

  return (
    <Flex
      align="center"
      className="rp-hit"
      gap={16}
      justify="space-between"
      onClick={() => onInsert(variable.key)}
      style={{
        background: token.colorFillQuaternary,
        borderRadius: token.borderRadius,
        cursor: 'pointer',
        padding: '8px 10px',
      }}
    >
      <Flex gap={2} style={{ minWidth: 0 }} vertical>
        <Flex align="center" gap={6}>
          <Text strong style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>
            {variable.key}
          </Text>
          {secret ? (
            <Tag color="gold" variant="filled">
              secret
            </Tag>
          ) : (
            <Tag variant="outlined">plain</Tag>
          )}
          {variable.source === 'compose' && <Tag color="blue">compose</Tag>}
        </Flex>
        <Flex align="center" gap={2} style={{ minWidth: 0 }}>
          <Text ellipsis style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }} type="secondary">
            {secret && !revealed ? masked : variable.value}
          </Text>
          {secret && (
            <Tooltip title={revealed ? 'Hide value' : 'Reveal value'}>
              <Button
                aria-label={revealed ? 'Hide value' : 'Reveal value'}
                icon={revealed ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                onClick={(event) => {
                  event.stopPropagation();
                  setRevealed((current) => !current);
                }}
                size="small"
                type="text"
              />
            </Tooltip>
          )}
          <Tooltip title={`Copy ${reference(variable.key)}`}>
            <Button
              aria-label={`Copy ${reference(variable.key)}`}
              icon={<CopyOutlined />}
              onClick={(event) => {
                event.stopPropagation();
                void navigator.clipboard.writeText(reference(variable.key));
                message.success(`Copied ${reference(variable.key)}`);
              }}
              size="small"
              type="text"
            />
          </Tooltip>
        </Flex>
      </Flex>

      <Flex align="center" gap={4} style={{ flex: 'none' }}>
        {alreadyReferenced && (
          <Tooltip
            title={`${reference(variable.key)} is already in this file — inserting again adds another reference`}
          >
            <CheckCircleOutlined style={{ color: token.colorSuccess }} />
          </Tooltip>
        )}
        <Button
          onClick={(event) => {
            // The row inserts on click too, so this must not bubble a second insert.
            event.stopPropagation();
            onInsert(variable.key);
          }}
          size="small"
          type="text"
        >
          Insert
        </Button>
      </Flex>
    </Flex>
  );
}

/**
 * The Compose editor, full screen: toolbar, the YAML file, and the variables a
 * deploy writes (the Environment's, this Compose's own laid over them) beside it so a reference can be dropped in without leaving the file.
 *
 * The page stays the owner of the draft — this component hands every edit back
 * through `onChange`. Both editors are mounted with the same Monaco `path`, so
 * there is one document and no copy to reconcile; the modal editor sets
 * `keepCurrentModel` so closing it cannot dispose the model the page editor is
 * still showing. Closing is not discarding: only `Discard` reloads the saved file.
 */
export function FullscreenEditor({
  compose,
  composeVariables,
  composeVariablesError,
  dirty,
  draft,
  environment,
  onChange,
  onClose,
  onDiscard,
  onSave,
  open,
  project,
  readOnly = false,
  saving,
}: {
  compose: Compose;
  /** This Compose's own variables; each overrides an Environment Variable of the same key. `undefined` until loaded. */
  composeVariables?: readonly ComposeVariable[];
  /** Why they could not load: the rail then cannot say which Environment values a Compose Variable overrides. */
  composeVariablesError?: string;
  dirty: boolean;
  draft: string;
  environment: Environment;
  onChange: (value: string) => void;
  onClose: () => void;
  onDiscard: () => void;
  onSave: () => void;
  open: boolean;
  project: Project;
  /** Only an admin changes the file (ADR-0032); anyone else reads it here. */
  readOnly?: boolean;
  saving: boolean;
}) {
  const { token } = theme.useToken();
  const navigate = useNavigate();
  const editorRef = useRef<MonacoEditorApi.IStandaloneCodeEditor | null>(null);
  const [query, setQuery] = useState('');
  const composeVariablesPending = composeVariables === undefined && !composeVariablesError;

  const all = useMemo(
    () => railVariables(environment.variables, composeVariables ?? NO_VARIABLES),
    [environment.variables, composeVariables],
  );
  const variables = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return needle ? all.filter((variable) => variable.key.toLowerCase().includes(needle)) : all;
  }, [all, query]);

  /**
   * Insert the reference, never the value: the file only ever holds `${KEY}`.
   * `forceMoveMarkers` leaves the cursor after the text, the undo stop keeps it
   * one ⌘Z, and focusing the editor is the only feedback — a toast per insert
   * would be noise.
   */
  const insert = (key: string) => {
    const editor = editorRef.current;
    const selection = editor?.getSelection();
    if (!editor || !selection) return;
    editor.executeEdits('variables', [{ forceMoveMarkers: true, range: selection, text: reference(key) }]);
    editor.pushUndoStop();
    editor.focus();
    editor.revealPositionInCenterIfOutsideViewport(editor.getPosition() ?? { column: 1, lineNumber: 1 });
  };

  return (
    <Modal
      closable={false}
      destroyOnHidden
      footer={null}
      onCancel={onClose}
      open={open}
      style={{ margin: 0, maxWidth: '100vw', paddingBottom: 0, top: 0 }}
      styles={{
        body: { display: 'flex', flexDirection: 'column', height: '100vh', minHeight: 0, overflow: 'hidden', padding: 0 },
        // antd's container is the visible panel: it carries the padding and the
        // radius that would otherwise frame the "full screen" in a 20px inset.
        container: { borderRadius: 0, padding: 0 },
        header: { display: 'none' },
        // Nothing here can overflow by design (the body is exactly 100vh), so the
        // wrapper must not add scrollbars of its own around a 100vw panel.
        wrapper: { overflow: 'hidden' },
      }}
      title={null}
      width="100vw"
    >
      <Flex
        align="center"
        justify="space-between"
        style={{
          borderBottom: `1px solid ${token.colorBorderSecondary}`,
          flex: 'none',
          gap: LAYOUT.gutterSmall,
          height: LAYOUT.barHeight,
          paddingInline: LAYOUT.gutterSmall,
        }}
      >
        <Flex align="center" gap={8} style={{ minWidth: 0 }}>
          <FileTextOutlined style={{ color: token.colorTextTertiary }} />
          <div style={{ minWidth: 0 }}>
            <Flex align="center" gap={8}>
              <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>docker-compose.yml</Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {project.name} / {environment.name} / {compose.name}
              </Text>
              <ComposeStatusTag lastDeployedAt={compose.lastDeployedAt} status={compose.status} />
            </Flex>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {readOnly
                ? 'Read-only: only an admin changes docker-compose.yml.'
                : 'The same file as the Compose page — edits are shared, and closing does not discard them.'}
            </Text>
          </div>
        </Flex>

        <Flex align="center" gap={8} style={{ flex: 'none' }}>
          {dirty && (
            <Text type="warning" style={{ fontSize: 12 }}>
              Modified
            </Text>
          )}
          <Tooltip title="Discard local edits and reload the saved file">
            <Button disabled={!dirty} icon={<UndoOutlined />} onClick={onDiscard}>
              Discard
            </Button>
          </Tooltip>
          <Button disabled={!dirty || readOnly} icon={<SaveOutlined />} loading={saving} onClick={onSave} type="primary">
            Save
          </Button>
          <Tooltip title="Close the full screen editor (Esc)">
            <Button aria-label="Close full screen" icon={<CloseOutlined />} onClick={onClose} type="text" />
          </Tooltip>
        </Flex>
      </Flex>

      {/* The modal is the bounded box: it owns the height and nothing inside it scrolls the page. */}
      <Flex style={{ flex: 1, minHeight: 0 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <YamlEditor
            keepCurrentModel
            onChange={onChange}
            onMount={(editor) => {
              editorRef.current = editor;
            }}
            path={`${project.id}-${environment.id}-${compose.id}.yml`}
            readOnly={readOnly}
            value={draft}
          />
        </div>

        {/* The one column that scrolls internally: a rail of variables on a full-height surface. */}
        <div
          style={{
            borderInlineStart: `1px solid ${token.colorBorderSecondary}`,
            flex: 'none',
            overflowY: 'auto',
            padding: LAYOUT.gutterSmall,
            width: 340,
          }}
        >
          <Text strong>Variables</Text>
          <Text type="secondary" style={{ display: 'block', fontSize: 12, marginTop: 4 }}>
            {environment.name} ·{' '}
            {composeVariablesPending ? 'loading…' : `${all.length} variable${all.length === 1 ? '' : 's'}`} · changes
            apply on next deploy
          </Text>
          {composeVariablesError && (
            <Alert
              description={`${composeVariablesError}. Values below are the Environment's and may be overridden by this Compose.`}
              message="Compose Variables could not be loaded"
              showIcon
              style={{ marginTop: 12 }}
              type="warning"
            />
          )}

          {composeVariablesPending && all.length === 0 ? null : all.length === 0 ? (
            <EmptyState
              action={
                <Button
                  onClick={() => {
                    onClose();
                    navigate(paths.environment(project.id, environment.id, 'variables'));
                  }}
                  type="link"
                >
                  Manage variables
                </Button>
              }
              description="Environment Variables are shared by every Compose here; a Compose Variable overrides one for this Compose only."
              title="No variables yet"
            />
          ) : (
            <>
              <Input
                allowClear
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Filter keys"
                style={{ marginBlock: 16 }}
                value={query}
              />
              {variables.length === 0 ? (
                <Text type="secondary" style={{ fontSize: 12 }}>
                  No keys match “{query}”.
                </Text>
              ) : (
                <Flex gap={8} vertical>
                  {variables.map((variable) => (
                    <VariableRow draft={draft} key={`${variable.source}:${variable.key}`} onInsert={insert} variable={variable} />
                  ))}
                </Flex>
              )}
            </>
          )}
        </div>
      </Flex>
    </Modal>
  );
}
