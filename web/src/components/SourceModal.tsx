import { InboxOutlined } from '@ant-design/icons';
import { Alert, Button, Divider, Flex, Input, Modal, Popconfirm, Segmented, Select, Switch, Typography, Upload } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { paths } from '../app/paths';
import { ApiError, api } from '../data/api';
import type { ComposePath } from '../data/api';
import type { ComposeSource, SshKey } from '../data/types';

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
 * is valid; the SSH key comes from the library; Test connection fills Branch; Save stores
 * branch, submodules and paths. Every API failure shows in an Alert inside the modal.
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
  /** The Source changed server side (connection tested): re-read it. */
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
  const [sparsePaths, setSparsePaths] = useState<string[]>([]);
  const [branches, setBranches] = useState<string[]>([]);
  const [keys, setKeys] = useState<SshKey[] | null>(null);
  const [keysFailed, setKeysFailed] = useState(false);
  const [sshKeyId, setSshKeyId] = useState<number | null>(null);
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
    setSparsePaths(current?.sparsePaths ?? []);
    setBranches([]);
    setSshKeyId(current?.sshKey?.id ?? null);
    setTestError('');
    setTestOk(false);
    setError('');
    setBusy('');
  }, [initialMode, open]);

  const fail = (reason: unknown) => setError(reason instanceof Error ? reason.message : 'Something went wrong');

  const loadKeys = async () => {
    setKeysFailed(false);
    try {
      const list = await api.listSshKeys();
      setKeys(list);
      // A key deleted elsewhere is no longer a choice.
      setSshKeyId((current) => (current !== null && !list.some((k) => k.id === current) ? null : current));
    } catch (reason) {
      setKeysFailed(true);
      fail(reason);
    }
  };

  useEffect(() => {
    if (open) {
      setKeys(null);
      void loadKeys();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- loadKeys only closes over setters
  }, [open]);

  /** The server no longer has the chosen key (deleted elsewhere): drop the choice and re-read the library. */
  const dropUnknownKey = (reason: unknown) => {
    if (
      reason instanceof ApiError &&
      reason.status === 400 &&
      (reason.message === 'unknown SSH key' || reason.message === 'Choose an SSH key first')
    ) {
      setSshKeyId(null);
      void loadKeys();
    }
  };

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
        sparsePaths,
        submodules,
        sshKeyId,
      });
      onSaved(saved, false);
    } catch (reason) {
      savedUrl.current = '';
      dropUnknownKey(reason);
      fail(reason);
    }
  };

  /**
   * Choosing a key saves it at once when the URL is already valid; otherwise the URL's own save carries it.
   * A failed save puts the previous choice back.
   */
  const chooseKey = async (id: number) => {
    const previous = sshKeyId;
    setSshKeyId(id);
    const trimmed = url.trim();
    if (!isValidSshUrl(trimmed)) {
      return;
    }
    setError('');
    try {
      const saved = await api.saveSource(location, {
        branch: sourceRef.current?.branch ?? '',
        kind: 'git',
        repoUrl: trimmed,
        sparsePaths,
        submodules,
        sshKeyId: id,
      });
      savedUrl.current = trimmed;
      // The server cleared the last test with the key change.
      setTestOk(false);
      setTestError('');
      setBranches([]);
      onSaved(saved, false);
    } catch (reason) {
      setSshKeyId(previous);
      dropUnknownKey(reason);
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

  const urlSaved = savedUrl.current !== '' && url.trim() === savedUrl.current;
  const canTest = urlSaved && sshKeyId !== null;
  const chosenKey = keys?.find((k) => k.id === sshKeyId);

  const run = async (name: string, action: () => Promise<void>) => {
    setBusy(name);
    setError('');
    try {
      await action();
    } catch (reason) {
      dropUnknownKey(reason);
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
      const saved = await api.saveSource(location, {
        branch,
        kind: 'git',
        repoUrl: url.trim(),
        sparsePaths,
        submodules,
        sshKeyId,
      });
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

            <Flex gap={4} vertical>
              <label htmlFor="source-ssh-key">
                <Text strong>SSH key</Text>
              </label>
              {keys !== null && keys.length === 0 && !keysFailed ? (
                <Flex align="center" gap={8} wrap>
                  <Text type="secondary">No SSH keys yet</Text>
                  <a href={paths.sshKeys} rel="noreferrer" target="_blank">
                    Create one in SSH keys
                  </a>
                  <Button onClick={() => void loadKeys()} size="small">
                    Reload
                  </Button>
                </Flex>
              ) : (
                <Flex gap={8}>
                  <Select
                    aria-label="SSH key"
                    id="source-ssh-key"
                    loading={keys === null && !keysFailed}
                    onChange={(id: number) => void chooseKey(id)}
                    options={(keys ?? []).map((k) => ({ label: k.name, value: k.id }))}
                    placeholder="Choose an SSH key"
                    style={{ flex: 1 }}
                    value={chosenKey ? chosenKey.id : undefined}
                  />
                  {keysFailed && (
                    <Button
                      onClick={() => {
                        setError('');
                        void loadKeys();
                      }}
                    >
                      Reload
                    </Button>
                  )}
                </Flex>
              )}
              {chosenKey && (
                <>
                  <Paragraph copyable={{ text: chosenKey.publicKey }} style={{ marginBottom: 0, wordBreak: 'break-all' }}>
                    <Text code>{chosenKey.publicKey}</Text>
                  </Paragraph>
                  <Text style={{ fontSize: 12 }} type="secondary">
                    Repository → Settings → Deploy keys → Add deploy key, leave write access off
                  </Text>
                </>
              )}
            </Flex>

            <Flex align="center" gap={8}>
              <Button disabled={!canTest} loading={busy === 'test'} onClick={() => void test()}>
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
            <Flex gap={4} vertical>
              <label htmlFor="source-paths">
                <Text strong>Paths</Text>
              </label>
              <Select
                aria-label="Paths"
                id="source-paths"
                mode="tags"
                onChange={setSparsePaths}
                open={false}
                placeholder="Whole repository"
                suffixIcon={null}
                tokenSeparators={[',', '\n']}
                value={sparsePaths}
              />
              <Text style={{ fontSize: 12 }} type="secondary">
                Only these directories are downloaded, plus the files at the repository root. They must hold every
                build context and file the YAML uses from ./src.
              </Text>
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
                  <Button danger loading={busy === 'reset'} onClick={() => void resetHost()}>
                    Reset host key
                  </Button>
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
