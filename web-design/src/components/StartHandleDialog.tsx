import { Alert, Col, Descriptions, Flex, InputNumber, Modal, Radio, Row, Tag, Typography } from 'antd';
import { useEffect, useRef } from 'react';
import { CODE_FONT_STACK } from '../app/theme';
import { useUiState } from '../app/uiState';
import { CodeValue } from './primitives';
import type { StartHandleInput } from '../data/store';
import type { UidSource, Volume } from '../data/types';
import { UID_SOURCE_LABEL } from '../data/types';
import { useSceneContainer } from '../scenes/scope';

const { Text } = Typography;

/**
 * Starting a Volume Handle.
 *
 * Everything the operator has to decide is on this one dialog, because none of it
 * can be decided for them safely:
 *
 *  - **uid/gid** — a handle that writes as the wrong uid leaves files the stack
 *    cannot read, and one that writes as root leaves files the stack cannot
 *    start with. The value is pre-filled from the resolution chain, and the
 *    dialog says which rung it came from, so an override is an informed one.
 *  - **read-only vs read-write** — enforced at the mount flag, not here. This is
 *    where the choice is *made*, not where it is *enforced*.
 *  - **the two warnings** — a bind source whose existence the panel cannot check,
 *    and a Compose whose Services are currently up.
 */
export function StartHandleDialog({
  loading,
  onCancel,
  onStart,
  open,
  resolved,
  runningServices,
  volume,
}: {
  loading?: boolean;
  onCancel: () => void;
  onStart: (input: StartHandleInput) => void;
  open: boolean;
  resolved: { gid: number; source: UidSource; uid: number };
  runningServices: string[];
  volume: Volume;
}) {
  const [mode, setMode] = useUiState<'ro' | 'rw'>('startHandle.mode', 'rw');
  const [uid, setUid] = useUiState('startHandle.uid', resolved.uid);
  const [gid, setGid] = useUiState('startHandle.gid', resolved.gid);
  const sceneContainer = useSceneContainer();

  // Ticket 02, decision D3: this reset used to run whenever `open` was true —
  // which on a cold load with a forced `?ui=startHandle.…` value is the mount
  // itself, wiping the forced value before it could paint. Keyed on the
  // false→true transition (and on a changed resolution while open) instead.
  // With no query string the behaviour is identical: the state starts at the
  // resolved defaults and every real open resets them.
  const previous = useRef({ open, gid: resolved.gid, uid: resolved.uid });
  useEffect(() => {
    const reopened = open && !previous.current.open;
    const reresolved = open && (previous.current.uid !== resolved.uid || previous.current.gid !== resolved.gid);
    previous.current = { open, gid: resolved.gid, uid: resolved.uid };
    if (reopened || reresolved) {
      setMode('rw');
      setUid(resolved.uid);
      setGid(resolved.gid);
    }
  }, [open, resolved.gid, resolved.uid, setGid, setMode, setUid]);

  const missing = volume.existence === 'absent';
  const source = uid !== resolved.uid || gid !== resolved.gid ? ('manual' as const) : resolved.source;

  return (
    <Modal
      cancelText="Cancel"
      confirmLoading={loading}
      destroyOnHidden
      getContainer={sceneContainer}
      okButtonProps={{ disabled: missing }}
      okText="Open Volume"
      onCancel={onCancel}
      onOk={() => onStart({ gid, mode, uid, uidSource: source })}
      open={open}
      title={`Open ${volume.declaredAs}`}
      width={640}
    >
      <Descriptions
        column={1}
        items={[
          {
            children: volume.kind === 'volume' ? <Tag>named volume</Tag> : <Tag color="geekblue">bind mount</Tag>,
            key: 'kind',
            label: 'Kind',
          },
          { children: <CodeValue copyable value={volume.source} />, key: 'source', label: 'Source on the host' },
          { children: <CodeValue copyable value={volume.target} />, key: 'target', label: 'Path in the container' },
          {
            children: (
              <Flex gap={4} wrap>
                {volume.services.map((service) => (
                  <Tag key={service}>{service}</Tag>
                ))}
              </Flex>
            ),
            key: 'services',
            label: 'Mounted by',
          },
        ]}
        labelStyle={{ width: 160 }}
        size="small"
      />

      {missing && (
        <Alert
          showIcon
          style={{ marginTop: 16 }}
          type="error"
          message="This source does not exist"
          description={
            <>
              Mounting it would create it, so the panel refuses. Deploy this Compose once, or create{' '}
              <Text code style={{ fontFamily: CODE_FONT_STACK }}>
                {volume.source}
              </Text>{' '}
              first if it is meant to be a bind mount.
            </>
          }
        />
      )}

      {volume.existence === 'unverifiable' && (
        <Alert
          showIcon
          style={{ marginTop: 16 }}
          type="warning"
          message="The panel cannot check whether this path exists"
          description={
            <>
              It is an absolute bind source outside the data directory, so the panel has no way to look at it without
              mounting it — and mounting it is what would create it. If it is not there, opening this Volume creates
              it as <Text strong>root:root 0755</Text> on the host.
            </>
          }
        />
      )}

      <Text strong style={{ display: 'block', marginBottom: 8, marginTop: 20 }}>
        Access
      </Text>
      <Radio.Group
        onChange={(event) => setMode(event.target.value as 'ro' | 'rw')}
        optionType="button"
        options={[
          { label: 'Read and write', value: 'rw' },
          { label: 'Read only', value: 'ro' },
        ]}
        value={mode}
      />
      <Text type="secondary" style={{ display: 'block', marginTop: 6 }}>
        {mode === 'rw'
          ? 'The Volume is mounted writable. Enforced on the mount, so nothing can write around it.'
          : 'The Volume is mounted read-only. Editing and uploading stay disabled.'}
      </Text>

      <Text strong style={{ display: 'block', marginBottom: 8, marginTop: 20 }}>
        Files are owned by
      </Text>
      <Row gutter={12}>
        <Col span={12}>
          <InputNumber
            addonBefore="uid"
            min={0}
            onChange={(value) => setUid(value ?? 0)}
            style={{ width: '100%' }}
            value={uid}
          />
        </Col>
        <Col span={12}>
          <InputNumber
            addonBefore="gid"
            min={0}
            onChange={(value) => setGid(value ?? 0)}
            style={{ width: '100%' }}
            value={gid}
          />
        </Col>
      </Row>
      <Text type="secondary" style={{ display: 'block', marginTop: 6 }}>
        Taken from {UID_SOURCE_LABEL[source]}.
      </Text>

      {uid === 0 && (
        <Alert
          showIcon
          style={{ marginTop: 16 }}
          type="warning"
          message="Running as root"
          description="Anything this handle creates on the Volume will be owned by root. A Service running as uid 999 may then fail to read it, or fail to start."
        />
      )}

      {mode === 'rw' && runningServices.length > 0 && (
        <Alert
          showIcon
          style={{ marginTop: 16 }}
          type="warning"
          message={`Writing here can corrupt a running stack`}
          description={
            <>
              {runningServices.length === 1 ? 'This Service is' : 'These Services are'} up right now:{' '}
              <Text strong>{runningServices.join(', ')}</Text>. Editing files a live process is using — a database's
              data directory above all — can corrupt them. There is no confirmation gate on the write itself: this is
              the warning, and it is the whole of it.
            </>
          }
        />
      )}
    </Modal>
  );
}
