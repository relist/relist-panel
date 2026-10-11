import { Button, Checkbox, Flex, Input, Modal, Table, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useState } from 'react';

import type { BuildTarget, DetectedDockerfile } from '../data/types';

const { Text } = Typography;

type Row = {
  context: string;
  dockerfile: string;
  path: string;
  replaceImage: boolean;
  service: string;
  target: string;
};

/**
 * The Dockerfiles found in the Source, as editable rows. "Insert into editor"
 * hands the checked rows to the page; nothing is saved here.
 */
export function DetectDockerfilesModal({
  existingServices,
  items,
  onClose,
  onInsert,
  onShowSnippets,
  open,
}: {
  existingServices: string[];
  items: DetectedDockerfile[];
  onClose: () => void;
  onInsert: (targets: BuildTarget[]) => void;
  onShowSnippets?: () => void;
  open: boolean;
}) {
  const [rows, setRows] = useState<Row[]>([]);
  const [checked, setChecked] = useState<string[]>([]);

  useEffect(() => {
    setRows(items.map((i) => ({ ...i, replaceImage: false, target: '' })));
    setChecked(items.map((i) => i.path));
  }, [items]);

  const patch = (path: string, change: Partial<Row>) =>
    setRows((current) => current.map((r) => (r.path === path ? { ...r, ...change } : r)));

  const insert = () => {
    const targets = rows
      .filter((r) => checked.includes(r.path) && r.service.trim() !== '')
      .map((r): BuildTarget => {
        const target: BuildTarget = {
          context: r.context.trim(),
          dockerfile: r.dockerfile,
          replaceImage: existingServices.includes(r.service.trim()) && r.replaceImage,
          service: r.service.trim(),
        };
        if (r.target.trim() !== '') {
          target.target = r.target.trim();
        }
        return target;
      });
    onInsert(targets);
  };

  const columns: TableColumnsType<Row> = [
    {
      dataIndex: 'service',
      render: (_: string, row) => (
        <Flex gap={4} vertical>
          <Input
            aria-label={`Service for ${row.path}`}
            onChange={(e) => patch(row.path, { service: e.target.value })}
            size="small"
            value={row.service}
          />
          {existingServices.includes(row.service.trim()) && (
            <Checkbox checked={row.replaceImage} onChange={(e) => patch(row.path, { replaceImage: e.target.checked })}>
              <Text style={{ fontSize: 12 }}>replace image</Text>
            </Checkbox>
          )}
        </Flex>
      ),
      title: 'Service',
      width: 190,
    },
    {
      dataIndex: 'path',
      render: (path: string) => <Text code style={{ fontSize: 12 }}>{path}</Text>,
      title: 'Dockerfile',
    },
    {
      dataIndex: 'context',
      render: (_: string, row) => (
        <Input
          aria-label={`Context for ${row.path}`}
          onChange={(e) => patch(row.path, { context: e.target.value })}
          size="small"
          value={row.context}
        />
      ),
      title: 'Context',
      width: 190,
    },
    {
      dataIndex: 'target',
      render: (_: string, row) => (
        <Input
          aria-label={`Stage for ${row.path}`}
          onChange={(e) => patch(row.path, { target: e.target.value })}
          placeholder="optional"
          size="small"
          value={row.target}
        />
      ),
      title: 'Stage',
      width: 120,
    },
  ];

  return (
    <Modal
      footer={
        <Flex align="center" justify="space-between">
          {onShowSnippets ? (
            <Button onClick={onShowSnippets} type="link">
              Build args &amp; secrets
            </Button>
          ) : (
            <span />
          )}
          <Flex gap={8}>
            <Button onClick={onClose}>Close</Button>
            <Button disabled={checked.length === 0} onClick={insert} type="primary">
              Insert into editor
            </Button>
          </Flex>
        </Flex>
      }
      onCancel={onClose}
      open={open}
      title="Detect Dockerfiles"
      width={820}
    >
      <Text type="secondary">
        Checked rows get a <Text code>build:</Text> block in the editor. Nothing is saved until you save the YAML.
      </Text>
      <Table
        columns={columns}
        dataSource={rows}
        locale={{ emptyText: 'No Dockerfiles found in the Source' }}
        pagination={false}
        rowKey="path"
        rowSelection={{
          getCheckboxProps: (row) => ({ 'aria-label': `Use ${row.path}` }),
          onChange: (keys) => setChecked(keys as string[]),
          selectedRowKeys: checked,
        }}
        size="small"
        style={{ marginTop: 12 }}
      />
    </Modal>
  );
}
