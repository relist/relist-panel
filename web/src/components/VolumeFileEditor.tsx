import { CloseOutlined, FileTextOutlined, SaveOutlined, UndoOutlined } from '@ant-design/icons';
import { Alert, App, Button, Flex, Modal, Tooltip, Typography, theme } from 'antd';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import type { VolumeFailure } from '../pages/volume-explorer-view';
import { editorSaveStatus, isDirty } from '../pages/volume-explorer-view';
import { TextFileEditor } from './TextFileEditor';

const { Text } = Typography;

/**
 * One Volume file in the editor, full screen: the same treatment the Compose
 * YAML screen gives its file (GUIDELINES §6) — the modal is the bounded box,
 * the editor fills it, and nothing inside scrolls the page.
 *
 * The page stays the owner of the draft: every edit goes back through
 * `onChange`, and `original` is the file as it was opened (or last saved), so
 * dirty is `draft !== original` and Discard is a reset, not a re-read. Closing
 * or discarding with unsaved changes asks first; the save's state (pending,
 * failed, clean) comes from the page's pending key and the API's answer, and a
 * failure stays on this screen as an `Alert`.
 */
export function VolumeFileEditor({
  draft,
  failure,
  onChange,
  onClose,
  onDiscard,
  onSave,
  open,
  original,
  path,
  saving,
}: {
  draft: string;
  /** The save's failure, shown in place — errors stay on their screen. */
  failure: VolumeFailure | undefined;
  onChange: (value: string) => void;
  onClose: () => void;
  onDiscard: () => void;
  onSave: () => void;
  open: boolean;
  /** The file as it was opened, or as it was last saved to. */
  original: string;
  path: string;
  saving: boolean;
}) {
  const { token } = theme.useToken();
  const { modal } = App.useApp();
  // The dirty and save-state decisions are the view helper's, not this
  // component's; the buttons and the confirmation read them.
  const dirty = isDirty(original, draft);
  const status = editorSaveStatus({ dirty, failure, saving });

  /**
   * Closing and discarding both throw away local edits, so both ask when
   * there are any. The dialog is the confirmation; the actions themselves
   * stay the page's.
   */
  const confirm = (action: 'discard' | 'close', run: () => void) => {
    modal.confirm({
      cancelText: 'Keep editing',
      content: (
        <>
          Your edits to <Text code>{path}</Text> are not saved.{' '}
          {action === 'discard'
            ? 'Discarding reloads the file as it was last saved.'
            : 'Closing does not write them to the Volume.'}
        </>
      ),
      okButtonProps: { danger: true },
      okText: action === 'discard' ? 'Discard changes' : 'Discard and close',
      onOk: run,
      title: action === 'discard' ? 'Discard unsaved changes?' : 'Close without saving?',
    });
  };

  const discard = () => (dirty ? confirm('discard', onDiscard) : onDiscard());
  const close = () => (dirty ? confirm('close', onClose) : onClose());

  return (
    <Modal
      closable={false}
      destroyOnHidden
      footer={null}
      onCancel={() => {
        if (!saving) {
          close();
        }
      }}
      open={open}
      style={{ margin: 0, maxWidth: '100vw', paddingBottom: 0, top: 0 }}
      styles={{
        body: {
          display: 'flex',
          flexDirection: 'column',
          height: '100vh',
          minHeight: 0,
          overflow: 'hidden',
          padding: 0,
        },
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
              <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{path}</Text>
            </Flex>
            <Text type="secondary" style={{ fontSize: 12 }}>
              Saving writes straight into the Volume; closing with unsaved changes asks first.
            </Text>
          </div>
        </Flex>

        <Flex align="center" gap={8} style={{ flex: 'none' }}>
          {dirty && (
            <Text type="warning" style={{ fontSize: 12 }}>
              Modified
            </Text>
          )}
          <Tooltip title="Discard local edits and reload the file as it was last saved">
            <Button disabled={!dirty || status === 'saving'} icon={<UndoOutlined />} onClick={discard}>
              Discard
            </Button>
          </Tooltip>
          <Button disabled={!dirty} icon={<SaveOutlined />} loading={saving} onClick={onSave} type="primary">
            Save
          </Button>
          <Tooltip title={saving ? 'The save is finishing' : 'Close the editor (Esc)'}>
            <Button
              aria-label="Close editor"
              disabled={status === 'saving'}
              icon={<CloseOutlined />}
              onClick={close}
              type="text"
            />
          </Tooltip>
        </Flex>
      </Flex>

      {failure && (
        <Alert
          description={failure.description}
          title={failure.message}
          showIcon
          style={{ flex: 'none', margin: LAYOUT.gutterSmall, marginBottom: 0 }}
          type="error"
        />
      )}

      {/* The modal is the bounded box: it owns the height and nothing inside it scrolls the page. */}
      <Flex style={{ flex: 1, minHeight: 0 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <TextFileEditor onChange={onChange} path={path} value={draft} />
        </div>
      </Flex>
    </Modal>
  );
}
