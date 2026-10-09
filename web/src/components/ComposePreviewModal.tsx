import { Alert, Button, Flex, Modal, Typography } from 'antd';
import { useLayoutEffect, useRef, useState } from 'react';
import { usePanel } from '../data/store';
import type { ComposePreview } from '../data/types';
import { YamlEditor } from './YamlEditor';

const { Text } = Typography;

/**
 * The resolved `docker-compose.yml` a deploy would write for the editor's
 * draft (GUIDELINES §4.6). It loads once per opening. The read-only editor
 * has its own `path`: sharing the page editor's model would let the preview
 * overwrite the draft.
 */
export function ComposePreviewModal({
  composeId,
  dirty,
  draft,
  editorPath,
  onClose,
  open,
}: {
  composeId: number;
  dirty: boolean;
  draft: string;
  editorPath: string;
  onClose: () => void;
  open: boolean;
}) {
  const { previewCompose } = usePanel();
  const [result, setResult] = useState<{ preview?: ComposePreview; error?: string }>();
  const request = useRef(0);
  // The draft as it was when the modal opened; later keystrokes don't refetch.
  const draftRef = useRef(draft);
  draftRef.current = draft;

  // A layout effect clears the last preview before the reopened modal
  // paints, so it never flashes the previous file.
  useLayoutEffect(() => {
    if (!open) {
      return;
    }
    const mine = ++request.current;
    setResult(undefined);
    previewCompose(composeId, draftRef.current).then(
      (preview) => {
        if (request.current === mine) {
          setResult({ preview });
        }
      },
      (reason: unknown) => {
        if (request.current === mine) {
          setResult({ error: reason instanceof Error ? reason.message : 'Could not preview the compose file' });
        }
      },
    );
  }, [composeId, open, previewCompose]);

  const preview = result?.preview;

  return (
    <Modal
      destroyOnHidden
      footer={
        <Button onClick={onClose} type="primary">
          Close
        </Button>
      }
      loading={open && !result}
      onCancel={onClose}
      open={open}
      title={
        <Flex align="center" gap={8}>
          Preview docker-compose.yml
          {preview && <Text copyable={{ text: preview.yaml, tooltips: ['Copy file', 'Copied'] }} />}
        </Flex>
      }
      width={{ lg: 900, md: 720, xs: '100%' }}
    >
      <Flex gap={12} vertical>
        <Text type="secondary">
          The file a deploy writes: your YAML with relist-network, the environment file and the Domains tab's Traefik labels
          added. Variables stay as <Text code>{'${KEY}'}</Text> references; keys are re-sorted and comments dropped.
        </Text>
        {dirty && <Alert showIcon title="Includes your unsaved changes." type="info" />}
        {result?.error && <Alert showIcon title={result.error} type="error" />}
        {preview?.warnings.map((warning) => (
          <Alert key={warning} showIcon title={warning} type="warning" />
        ))}
        {preview && (
          <div style={{ height: '60vh' }}>
            <YamlEditor path={`${editorPath}.preview.yml`} readOnly value={preview.yaml} />
          </div>
        )}
      </Flex>
    </Modal>
  );
}
