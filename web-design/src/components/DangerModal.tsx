import { Alert, Input, Modal, Typography } from 'antd';
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { useSceneContainer } from '../scenes/scope';

const { Text } = Typography;

/**
 * Destructive confirmations. Deleting a Compose removes containers, networks and
 * volumes on the host, so the design asks the operator to type the name: the
 * extra keystrokes are the point.
 */
export function DangerModal({
  confirmWord,
  consequences,
  description,
  loading,
  okText,
  onCancel,
  onConfirm,
  open,
  title,
}: {
  confirmWord: string;
  consequences?: ReactNode;
  description?: ReactNode;
  loading?: boolean;
  okText: string;
  onCancel: () => void;
  onConfirm: () => void;
  open: boolean;
  title: string;
}) {
  const [typed, setTyped] = useState('');
  // The frame box inside a scene scope, `undefined` (the viewport) standalone.
  const sceneContainer = useSceneContainer();

  useEffect(() => {
    if (open) setTyped('');
  }, [open]);

  const matches = typed.trim() === confirmWord;

  return (
    <Modal
      confirmLoading={loading}
      destroyOnHidden
      getContainer={sceneContainer}
      okButtonProps={{ danger: true, disabled: !matches }}
      okText={okText}
      onCancel={onCancel}
      onOk={onConfirm}
      open={open}
      title={title}
      width={520}
    >
      {description && <p style={{ marginTop: 0 }}>{description}</p>}
      {consequences}
      <div style={{ marginTop: 16 }}>
        <Text strong style={{ display: 'block', marginBottom: 8 }}>
          Type {confirmWord} to confirm
        </Text>
        <Input
          autoComplete="off"
          onChange={(event) => setTyped(event.target.value)}
          placeholder={confirmWord}
          value={typed}
        />
      </div>
      <Alert
        banner
        showIcon
        style={{ margin: '16px -24px -20px' }}
        type="warning"
        message="This action cannot be undone."
      />
    </Modal>
  );
}
