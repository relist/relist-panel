import { InboxOutlined } from '@ant-design/icons';
import { Alert, Button, Divider, Flex, Input, Modal, Popconfirm, Segmented, Select, Switch, Typography, Upload } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import type { ComposeSource } from '../data/types';

const { Dragger } = Upload;
const { Paragraph, Text } = Typography;

type Mode = 'git' | 'zip';

/** An SSH remote: `git@host:owner/repo.git` or `ssh://…`. Never something that could read as an option. */
export function isValidSshUrl(value: string): boolean {
  return /^(git@[\w.-]+:[\w./~-]+|ssh:\/\/[^\s-][^\s]*)$/.test(value.trim());
}

const SAVE_URL_DELAY_MS = 600;

/**
 * Configures a Compose's Source. The Git flow saves the SSH URL as soon as it
 * is valid so the Deploy key exists; Test connection fills Branch; Save stores
 * branch and submodules. Every API failure shows in an Alert inside the modal.
 * `onSaved(source, firstTime)`: `firstTime` is true when this save or upload
 * created the Source.
 */
export function SourceModal({
  initialMode,
  location,
  onChanged,
  onClose,
  onRemoved,
  onSaved,
  open,
  source,
}: {
  initialMode?: Mode;
  location: ComposePath;
  /** The Source changed server side (key regenerated, connection tested): re-read it. */
  onChanged: () => void;
  onClose: () => void;
  onRemoved: () => void;
  onSaved: (source: ComposeSource, firstTime: boolean) => void;
  open: boolean;
  source: ComposeSource | null;
}) {
  const [mode, setMode] = useState<Mode>('git');
  const [url, setUrl] = useState('');
  const [branch, setBranch] = useState('');
  const [submodules, setSubmodules] = useState(false);
  const [branches, setBranches] = useState<string[]>([]);
  const [publicKey, setPublicKey] = useState('');
  const [testError, setTestError] = useState('');
  const [testOk, setTestOk] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const wasNew = useRef(true);
  const savedUrl = useRef('');

  // Each time the modal opens it starts from the Source as it is then.
  const sourceRef = useRef(source);
  sourceRef.current = source;
  useEffect(() => {
    if (!open) {
      return;
    }
    const current = sourceRef.current;
    wasNew.current = current === null;
    setMode(initialMode ?? (current?.kind === 'zip' ? 'zip' : 'git'));
    const repoUrl = current?.kind === 'git' ? current.repoUrl : '';
    setUrl(repoUrl);
    savedUrl.current = repoUrl;
    setBranch(current?.branch ?? '');
    setSubmodules(current?.submodules ?? false);
    setBranches([]);
    setPublicKey(current?.sshKey?.publicKey ?? '');
    setTestError('');
    setTestOk(false);
    setError('');
    setBusy('');
  }, [initialMode, open]);

  const fail = (reason: unknown) => setError(reason instanceof Error ? reason.message : 'Something went wrong');

  const persistUrl = async (value: string) => {
    const trimmed = value.trim();
    if (!isValidSshUrl(trimmed) || trimmed === savedUrl.current) {
      return;
    }
    savedUrl.current = trimmed;
    setError('');
    try {
      const saved = await api.saveSource(location, {
        branch: sourceRef.current?.branch ?? '',
        kind: 'git',
        repoUrl: trimmed,
        submodules,
        sshKeyId: sourceRef.current?.sshKey?.id ?? null,
      });
      setPublicKey(saved.sshKey?.publicKey ?? '');
      onSaved(saved, false);
    } catch (reason) {
      savedUrl.current = '';
      fail(reason);
    }
  };

  const persistRef = useRef(persistUrl);
  persistRef.current = persistUrl;
  useEffect(() => {
    if (!open || !isValidSshUrl(url) || url.trim() === savedUrl.current) {
      return;
    }
    const timer = setTimeout(() => void persistRef.current(url), SAVE_URL_DELAY_MS);
    return () => clearTimeout(timer);
  }, [open, url]);

  const urlSaved = savedUrl.current !== '' && url.trim() === savedUrl.current && publicKey !== '';

  const run = async (name: string, action: () => Promise<void>) => {
    setBusy(name);
    setError('');
    try {
      await action();
    } catch (reason) {
      fail(reason);
    } finally {
      setBusy('');
    }
  };

  const test = () =>
    run('test', async () => {
      const result = await api.testSource(location);
      onChanged();
      setTestOk(result.ok);
      setTestError(result.ok ? '' : result.error || 'Connection failed');
      if (result.ok) {
        setBranches(result.branches);
        if (!result.branches.includes(branch)) {
          setBranch(result.branches.find((b) => b === 'main' || b === 'master') ?? result.branches[0] ?? '');
        }
      }
    });

  const save = () =>
    run('save', async () => {
      const saved = await api.saveSource(location, { branch, kind: 'git', repoUrl: url.trim(), submodules, sshKeyId: source?.sshKey?.id ?? null });
      onSaved(saved, wasNew.current);
      onClose();
    });

  const upload = (file: File) =>
    run('upload', async () => {
      const saved = await api.uploadSource(location, file);
      onSaved(saved, wasNew.current);
      onClose();
    });

  const resetHost = () =>
    run('reset', async () => {
      await api.resetKnownHosts(location);
    });

  const remove = () =>
    run('remove', async () => {
      await api.deleteSource(location);
      onRemoved();
      onClose();
    });

  const branchOptions = [...new Set([...(branch ? [branch] : []), ...branches])].map((b) => ({ label: b, value: b }));

  return (
    <Modal footer={null} onCancel={onClose} open={open} title="Source" width={600}>
      <Flex gap={16} vertical>
        <Segmented
          block
          onChange={(value) => {
            setMode(value as Mode);
            setError('');
          }}
          options={[
            { label: 'Git repository', value: 'git' },
            { label: 'Upload zip', value: 'zip' },
          ]}
          value={mode}
        />
        {error && <Alert showIcon title={error} type="error" />}

        {mode === 'git' ? (
          <Flex gap={12} vertical>
            <Flex gap={4} vertical>
              <label htmlFor="source-ssh-url">
                <Text strong>SSH URL</Text>
              </label>
              <Input
                id="source-ssh-url"
                onBlur={() => void persistUrl(url)}
                onChange={(e) => {
                  setUrl(e.target.value);
                }}
                placeholder="git@github.com:acme/shop.git"
                value={url}
              />
            </Flex>

            {publicKey && (
              <Flex gap={4} vertical>
                <Text strong>Deploy key</Text>
                <Paragraph copyable={{ text: publicKey }} style={{ marginBottom: 0, wordBreak: 'break-all' }}>
                  <Text code>{publicKey}</Text>
                </Paragraph>
                <Text style={{ fontSize: 12 }} type="secondary">
                  Repository → Settings → Deploy keys → Add deploy key, leave write access off
                </Text>
              </Flex>
            )}

            <Flex align="center" gap={8}>
              <Button disabled={!urlSaved} loading={busy === 'test'} onClick={() => void test()}>
                Test connection
              </Button>
              {testOk && <Text type="success">Connected</Text>}
            </Flex>
            {testError && <Text type="danger">{testError}</Text>}

            <Flex gap={4} vertical>
              <label htmlFor="source-branch">
                <Text strong>Branch</Text>
              </label>
              <Select
                aria-label="Branch"
                disabled={!urlSaved}
                id="source-branch"
                onChange={setBranch}
                options={branchOptions}
                placeholder="Test the connection to list branches"
                value={branch || undefined}
              />
            </Flex>
            <Flex align="center" gap={8}>
              <Switch aria-label="Submodules" checked={submodules} onChange={setSubmodules} />
              <Text>Submodules</Text>
            </Flex>
            <Flex justify="flex-end">
              <Button
                disabled={!urlSaved || branch === ''}
                loading={busy === 'save'}
                onClick={() => void save()}
                type="primary"
              >
                Save
              </Button>
            </Flex>
          </Flex>
        ) : (
          <Dragger
            accept=".zip"
            beforeUpload={(file) => {
              void upload(file);
              return false;
            }}
            disabled={busy === 'upload'}
            maxCount={1}
            showUploadList={false}
          >
            <p className="ant-upload-drag-icon">
              <InboxOutlined />
            </p>
            <p className="ant-upload-text">Click or drag a .zip here</p>
            <p className="ant-upload-hint">Up to 500 MiB</p>
            <p className="ant-upload-hint">Replaces all code in the Source</p>
          </Dragger>
        )}

        {source && (
          <>
            <Divider style={{ margin: 0 }} />
            <Flex gap={8} vertical>
              <Text strong type="danger">
                Danger area
              </Text>
              <Flex gap={8} wrap>
                {source.kind === 'git' && (
                  <>
                    <Button danger loading={busy === 'reset'} onClick={() => void resetHost()}>
                      Reset host key
                    </Button>
                  </>
                )}
                <Popconfirm
                  description="The code in src/ is deleted. The YAML is not changed."
                  okButtonProps={{ danger: true }}
                  okText="Remove"
                  onConfirm={() => void remove()}
                  title="Remove the Source?"
                >
                  <Button danger loading={busy === 'remove'}>
                    Remove source
                  </Button>
                </Popconfirm>
              </Flex>
            </Flex>
          </>
        )}
      </Flex>
    </Modal>
  );
}
