import {
  ArrowLeftOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  ExportOutlined,
  FileOutlined,
  FolderOutlined,
  LinkOutlined,
  LockOutlined,
  MoreOutlined,
  PlusOutlined,
  ReloadOutlined,
  StopOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App,
  Button,
  Card,
  Descriptions,
  Dropdown,
  Flex,
  Input,
  Modal,
  Popconfirm,
  Radio,
  Result,
  Skeleton,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { navigate, paths } from '../app/router';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { useNow } from '../app/useNow';
import { CodeValue, EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { StartHandleDialog } from '../components/StartHandleDialog';
import { TextFileEditor } from '../components/TextFileEditor';
import { IDLE_TIMEOUT_MINUTES, usePanel } from '../data/store';
import type { Volume, VolumeEntry } from '../data/types';
import { findComposeLocation, formatBytes, formatOwner, idleMinutesLeft } from '../data/types';
import { EDITOR_MAX_BYTES, summarizeArchive, UPLOAD_MAX_BYTES } from '../data/volumeMocks';
import { useSceneContainer } from '../scenes/scope';

const { Text, Paragraph } = Typography;

/**
 * The Volume Explorer.
 *
 * A Volume's files, as the filesystem really holds them. Three rules shape the
 * whole screen:
 *
 *  - **A symlink is shown, described, and followed only when it stays inside the
 *    Volume.** Hiding one would misrepresent the data — an operator who cannot
 *    see it cannot understand why a path exists — so it is listed with its
 *    target, and only the traversal into it is refused.
 *  - **Nothing is silently resolved.** Paths are shown as the host holds them,
 *    and an error carries the helper's own words rather than a generic failure.
 *  - **The page scrolls as one document.** The file list runs long; the file
 *    editor is the only bounded box on the screen.
 */
export function VolumeExplorerPage({
  composeId,
  environmentId,
  path,
  projectId,
  volumeId,
}: {
  composeId: number;
  environmentId: number;
  path: string;
  projectId: number;
  volumeId: string;
}) {
  const {
    handles,
    isPending,
    listDir,
    projects,
    readVolumeFile,
    resolveComposeVolumes,
    startHandle,
    stopHandle,
    writeVolumeFile,
  } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const now = useNow();
  const sceneContainer = useSceneContainer();

  const location = findComposeLocation(projects, composeId);
  const compose = location?.compose;
  const environment = location?.environment;
  const project = location?.project;
  const handle = handles[volumeId];

  const [volumes, setVolumes] = useState<Volume[]>();
  const [resolutionError, setResolutionError] = useState<string>();
  const [entries, setEntries] = useState<VolumeEntry[]>();
  const [browseError, setBrowseError] = useState<string>();
  const [startOpen, setStartOpen] = useState(false);
  const [editing, setEditing] = useState<{ contents: string; path: string }>();
  const [editorTooLarge, setEditorTooLarge] = useState<VolumeEntry>();
  const [uploadError, setUploadError] = useState<string>();
  const [downloadOpen, setDownloadOpen] = useState(false);
  const [newEntry, setNewEntry] = useState<'file' | 'dir'>();
  const [newEntryName, setNewEntryName] = useState('');
  const fileInput = useRef<HTMLInputElement>(null);

  const goTo = useCallback(
    (next: string) => navigate(paths.volumeExplorer(projectId, environmentId, composeId, volumeId, next)),
    [composeId, environmentId, projectId, volumeId],
  );

  // Volumes come from the CLI, so they are resolved rather than assumed — and the
  // resolution can fail outright, which is one of the states this screen shows.
  useEffect(() => {
    let cancelled = false;
    void resolveComposeVolumes(composeId).then((result) => {
      if (cancelled) return;
      if (result.ok) {
        setVolumes(result.volumes);
        setResolutionError(undefined);
      } else {
        setResolutionError(result.error);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [composeId, resolveComposeVolumes]);

  const load = useCallback(async () => {
    setBrowseError(undefined);
    setEntries(undefined);
    try {
      setEntries(await listDir(volumeId, path));
    } catch (error) {
      setBrowseError(error instanceof Error ? error.message : String(error));
    }
  }, [listDir, path, volumeId]);

  // Keyed on *whether* a handle is open, never on the handle object: every read
  // pushes `lastUsedAt`, which mints a new object, and an effect keyed on the
  // object would re-read the directory in a loop — each read refreshing the clock
  // that triggered it. React does not even complain, because the awaits break the
  // synchronous update chain; the screen just sits on its skeleton forever.
  const handleOpen = Boolean(handle);
  useEffect(() => {
    if (handleOpen) {
      void load();
    }
  }, [handleOpen, load]);

  // A handle expiring mid-visit is the one transition the operator did not ask
  // for, so it gets said out loud rather than silently emptying the screen.
  const [expiredHere, setExpiredHere] = useState(false);
  const hadHandle = useRef(false);
  useEffect(() => {
    if (handleOpen) {
      hadHandle.current = true;
      setExpiredHere(false);
    } else if (hadHandle.current) {
      setExpiredHere(true);
    }
  }, [handleOpen]);

  const volume = volumes?.find((candidate) => candidate.id === volumeId);
  const loadingList = isPending(`browse:${volumeId}:${path}`);
  const remaining = handle ? idleMinutesLeft(handle, now, IDLE_TIMEOUT_MINUTES) : undefined;
  const runningServices =
    compose?.services.filter((service) => service.state === 'running').map((service) => service.name) ?? [];
  const writable = handle?.mode === 'rw';
  const crumbs = path.split('/').filter(Boolean);
  const openInNewTab = () => window.open(paths.volumeExplorer(projectId, environmentId, composeId, volumeId, path), '_blank');

  const openFile = async (entry: VolumeEntry) => {
    if (entry.size > EDITOR_MAX_BYTES) {
      setEditorTooLarge(entry);
      return;
    }
    setEditorTooLarge(undefined);
    const filePath = joinPath(path, entry.name);
    const contents = await readVolumeFile(volumeId, filePath);
    setEditing({ contents, path: filePath });
  };

  const saveFile = async () => {
    if (!editing) return;
    await writeVolumeFile(volumeId, editing.path, editing.contents);
    message.success(`Saved ${editing.path.split('/').pop()}`);
    setEditing(undefined);
  };

  const upload = async (file: File) => {
    setUploadError(undefined);
    if (file.size > UPLOAD_MAX_BYTES) {
      setUploadError(`${file.name} is ${formatBytes(file.size)} — the upload limit is ${formatBytes(UPLOAD_MAX_BYTES)}.`);
      return;
    }
    await writeVolumeFile(volumeId, joinPath(path, file.name), '');
    // The mock's tree is static, so the new entry is added here: the point of the
    // pass is that the result of a write is visible without a reload.
    setEntries((current) => [
      ...(current ?? []),
      { gid: handle?.gid ?? 999, kind: 'file', mode: '0644', modifiedAt: new Date().toISOString(), name: file.name, size: file.size, uid: handle?.uid ?? 999 },
    ]);
    message.success(`Uploaded ${file.name}`);
  };

  const createEntry = () => {
    const name = newEntryName.trim();
    setEntries((current) => [
      ...(current ?? []),
      {
        gid: handle?.gid ?? 999,
        kind: newEntry === 'dir' ? 'dir' : 'file',
        mode: newEntry === 'dir' ? '0755' : '0644',
        modifiedAt: new Date().toISOString(),
        name,
        size: newEntry === 'dir' ? 4096 : 0,
        uid: handle?.uid ?? 999,
      },
    ]);
    message.success(`Created ${joinPath(path, name)}`);
    setNewEntry(undefined);
  };

  const header = (
    <PageHeader
      breadcrumb={[
        { href: paths.overview, title: 'Overview' },
        { href: paths.environment(projectId, environmentId), title: project?.name ?? 'Project' },
        { href: paths.compose(projectId, environmentId, composeId, 'volumes'), title: compose?.name ?? 'Compose' },
        { title: volume?.declaredAs ?? 'Volume' },
      ]}
      description={
        <Flex align="center" gap={6} wrap>
          <span>Files in this Volume</span>
          {volume && (
            <>
              <span>·</span>
              <Text style={{ fontFamily: CODE_FONT_STACK }}>{volume.source}</Text>
            </>
          )}
        </Flex>
      }
      extra={
        <Flex align="center" gap={8} wrap>
          {handle ? (
            <>
              <Tag color={handle.mode === 'rw' ? 'processing' : 'default'} icon={handle.mode === 'ro' ? <LockOutlined /> : undefined}>
                {handle.mode === 'rw' ? 'Read and write' : 'Read only'}
              </Tag>
              <Text type="secondary">
                uid {handle.uid}:{handle.gid}
              </Text>
              {remaining !== undefined && (
                <Tooltip title={`Auto-stops after ${IDLE_TIMEOUT_MINUTES} minutes of disuse. Any read or write restarts the clock.`}>
                  <Tag>closes in {remaining} min</Tag>
                </Tooltip>
              )}
              <Button
                icon={<StopOutlined />}
                loading={isPending(`handle-stop:${volumeId}`)}
                onClick={() =>
                  void stopHandle(volumeId).then(() => message.success(`Closed ${volume?.declaredAs ?? 'the Volume'}`))
                }
              >
                Close
              </Button>
            </>
          ) : (
            <Button icon={<FolderOutlined />} onClick={() => setStartOpen(true)} type="primary">
              Open this Volume
            </Button>
          )}
          <Tooltip title="This screen is meant to be opened in its own tab">
            <Button icon={<ExportOutlined />} onClick={openInNewTab} />
          </Tooltip>
        </Flex>
      }
      tags={volume && <Tag color={volume.kind === 'bind' ? 'geekblue' : undefined}>{volume.kind === 'bind' ? 'bind mount' : 'named volume'}</Tag>}
      title={volume?.declaredAs ?? 'Volume'}
    />
  );

  if (!compose || !environment || !project || !location) {
    return (
      <Result
        extra={
          <Button onClick={() => navigate(paths.projects)} type="primary">
            Back to Projects
          </Button>
        }
        status="404"
        subTitle="It may have been deleted, or the link is stale."
        title="Compose not found"
      />
    );
  }

  return (
    <>
      {header}
      {body()}
      <StartHandleDialog
        loading={isPending(`handle-start:${volumeId}`)}
        onCancel={() => setStartOpen(false)}
        onStart={(input) => {
          void startHandle(volumeId, input).then(() => {
            setStartOpen(false);
            message.success(`Opened ${volume?.declaredAs ?? 'the Volume'}`);
          });
        }}
        open={startOpen}
        resolved={{ gid: handle?.gid ?? 999, source: handle?.uidSource ?? 'container-inspect', uid: handle?.uid ?? 999 }}
        runningServices={runningServices}
        volume={volume ?? PLACEHOLDER_VOLUME}
      />
    </>
  );

  function body(): ReactNode {
    if (resolutionError) {
      return (
        <Card variant="outlined">
          <Result
            extra={
              <Button onClick={() => navigate(paths.compose(projectId, environmentId, composeId, 'volumes'))}>
                Back to Volumes
              </Button>
            }
            status="warning"
            subTitle={
              <>
                This Compose's Volumes cannot be listed until it has been deployed once. Resolution goes through{' '}
                <Text code>docker compose config</Text>, which needs the environment file a deploy writes.
              </>
            }
            title="Volumes are not resolvable yet"
          />
          <CodeValue copyable label="docker compose:" value={resolutionError} />
        </Card>
      );
    }
    if (!volumes) {
      return <Skeleton active paragraph={{ rows: 6 }} title={false} />;
    }
    if (!volume) {
      return (
        <Result
          extra={
            <Button onClick={() => navigate(paths.compose(projectId, environmentId, composeId, 'volumes'))} type="primary">
              Back to Volumes
            </Button>
          }
          status="404"
          subTitle="It may have been removed from the Compose since this link was made."
          title="Volume not found"
        />
      );
    }
    if (!handle) {
      // Not an error: a handle exists only while someone has it open, and opening
      // one is an explicit act.
      return (
        <Card variant="outlined">
          {expiredHere && (
            <Alert
              closable
              description="It stopped itself after a period of disuse. Nothing was changed, and reopening it costs one click."
              message="This Volume Handle closed on its own"
              style={{ marginBottom: LAYOUT.gutterSmall }}
              type="info"
            />
          )}
          <EmptyState
            action={
              <Button icon={<FolderOutlined />} onClick={() => setStartOpen(true)} type="primary">
                Open this Volume
              </Button>
            }
            description="A Volume Handle is opened on purpose and closed on purpose. Nothing is mounted until you open one, and it stops itself after a period of disuse."
            title="No handle open"
          />
        </Card>
      );
    }
    if (browseError) {
      return (
        <Card variant="outlined">
          <Result
            extra={<Button onClick={() => void load()}>Try again</Button>}
            status="error"
            subTitle="The Volume Handle could not read this directory."
            title="Could not read this directory"
          />
          <CodeValue copyable label="helper:" value={browseError} />
        </Card>
      );
    }
    if (loadingList || !entries) {
      return <Skeleton active paragraph={{ rows: 6 }} title={false} />;
    }

    return (
      <>
        {handle.mode === 'ro' && (
          <Alert
            description="This handle was opened read-only, so editing, uploading and deleting are off. The Volume is mounted read-only, so nothing can write around this."
            icon={<LockOutlined />}
            message="Read only"
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="info"
          />
        )}

        {writable && runningServices.length > 0 && (
          <Alert
            description={
              <>
                Editing files a live process is using can corrupt them — a database's data directory is the one that goes
                wrong quietly. There is no confirmation gate on a write; this banner is the whole warning, so it says what
                is at risk instead of telling you to be careful.
              </>
            }
            message={`This Compose is running — writing can corrupt ${runningServices.join(', ')}`}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="warning"
          />
        )}

        {editorTooLarge && (
          <Alert
            closable
            description={
              <>
                <Text strong>{editorTooLarge.name}</Text> is {formatBytes(editorTooLarge.size)}. The editor opens files up
                to {formatBytes(EDITOR_MAX_BYTES)}; past that it is unusable anyway. Download it instead.
              </>
            }
            message="Too large to edit"
            onClose={() => setEditorTooLarge(undefined)}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="info"
          />
        )}

        {uploadError && (
          <Alert
            closable
            description={uploadError}
            message="Upload refused"
            onClose={() => setUploadError(undefined)}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="error"
          />
        )}

        <Card
          extra={
            <Flex gap={8} wrap>
              <Button
                disabled={!writable}
                icon={<PlusOutlined />}
                onClick={() => {
                  setNewEntryName('');
                  setNewEntry('file');
                }}
              >
                New file
              </Button>
              <Button
                disabled={!writable}
                icon={<FolderOutlined />}
                onClick={() => {
                  setNewEntryName('');
                  setNewEntry('dir');
                }}
              >
                New folder
              </Button>
              <Button disabled={!writable} icon={<UploadOutlined />} onClick={() => fileInput.current?.click()}>
                Upload
              </Button>
              <Button icon={<DownloadOutlined />} onClick={() => setDownloadOpen(true)}>
                Download
              </Button>
              <Tooltip title="Re-read this directory">
                <Button icon={<ReloadOutlined />} onClick={() => void load()} />
              </Tooltip>
            </Flex>
          }
          title={<Crumbs crumbs={crumbs} onGo={goTo} root={volume.declaredAs} />}
          variant="outlined"
        >
          <Table
            columns={columns(openFile, writable, message, token)}
            dataSource={entries}
            pagination={false}
            rowKey={(entry) => entry.name}
            size="middle"
            onRow={(entry) => ({
              onClick: () => {
                if (entry.kind === 'dir') {
                  goTo(joinPath(path, entry.name));
                } else if (entry.kind === 'symlink' && entry.linkEscapes) {
                  message.warning(`${entry.name} points outside this Volume, so it is not followed.`);
                } else if (entry.kind === 'symlink') {
                  goTo(joinPath(path, entry.name));
                } else {
                  void openFile(entry);
                }
              },
              style: { cursor: 'pointer' },
            })}
          />
          <input
            hidden
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) void upload(file);
              event.target.value = '';
            }}
            ref={fileInput}
            type="file"
          />
        </Card>

        {editing && (
          <Card
            extra={
              <Space>
                <Button onClick={() => setEditing(undefined)}>Cancel</Button>
                <Button
                  disabled={!writable}
                  loading={isPending(`write:${volumeId}:${editing.path}`)}
                  onClick={() => void saveFile()}
                  type="primary"
                >
                  Save
                </Button>
              </Space>
            }
            style={{ marginTop: LAYOUT.gutter }}
            styles={{ body: { height: LAYOUT.volumeEditorHeight, padding: 0 } }}
            title={
              <Flex align="center" gap={8}>
                <EditOutlined />
                <Text style={{ fontFamily: CODE_FONT_STACK }}>{editing.path}</Text>
              </Flex>
            }
            variant="outlined"
          >
            <TextFileEditor
              onChange={(contents) => setEditing({ contents, path: editing.path })}
              path={editing.path}
              readOnly={!writable}
              value={editing.contents}
            />
          </Card>
        )}

        <Modal
          cancelText="Cancel"
          getContainer={sceneContainer}
          okButtonProps={{ disabled: !newEntryName.trim() }}
          okText={newEntry === 'dir' ? 'Create folder' : 'Create file'}
          onCancel={() => setNewEntry(undefined)}
          onOk={createEntry}
          open={Boolean(newEntry)}
          title={newEntry === 'dir' ? 'New folder' : 'New file'}
        >
          <Paragraph type="secondary" style={{ marginTop: 0 }}>
            Created inside <Text code>{path || '/'}</Text>.
          </Paragraph>
          <Input
            autoFocus
            onChange={(event) => setNewEntryName(event.target.value)}
            placeholder={newEntry === 'dir' ? 'invoices' : 'README.txt'}
            value={newEntryName}
          />
          <Text type="secondary" style={{ display: 'block', marginTop: 8 }}>
            Owned by {formatOwner(handle.uid, handle.gid)} when it is written.
          </Text>
        </Modal>

        <DownloadDialog
          onClose={() => setDownloadOpen(false)}
          open={downloadOpen}
          path={path}
          rootName={volume.declaredAs}
          volumeId={volumeId}
        />
      </>
    );
  }
}

const PLACEHOLDER_VOLUME: Volume = {
  composeId: 0,
  declaredAs: 'Volume',
  existence: 'present',
  id: '',
  kind: 'volume',
  readOnly: false,
  services: [],
  source: '',
  target: '',
};

function Crumbs({ crumbs, onGo, root }: { crumbs: string[]; onGo: (path: string) => void; root: string }) {
  return (
    <Flex align="center" gap={8} wrap>
      <Button
        disabled={crumbs.length === 0}
        icon={<ArrowLeftOutlined />}
        onClick={() => onGo(crumbs.slice(0, -1).join('/'))}
        size="small"
        type="text"
      />
      <Text onClick={() => onGo('')} strong={crumbs.length === 0} style={{ cursor: 'pointer', fontFamily: CODE_FONT_STACK }}>
        {root}
      </Text>
      {crumbs.map((crumb, index) => (
        <Flex align="center" gap={8} key={`${crumb}-${index}`}>
          <Text type="secondary">/</Text>
          <Text
            onClick={() => onGo(crumbs.slice(0, index + 1).join('/'))}
            strong={index === crumbs.length - 1}
            style={{ cursor: 'pointer', fontFamily: CODE_FONT_STACK }}
          >
            {crumb}
          </Text>
        </Flex>
      ))}
    </Flex>
  );
}

function columns(
  openFile: (entry: VolumeEntry) => void,
  writable: boolean,
  message: { warning: (text: string) => void },
  token: ReturnType<typeof theme.useToken>['token'],
): ColumnsType<VolumeEntry> {
  return [
    {
      dataIndex: 'name',
      render: (_: unknown, entry) => (
        <Flex align="center" gap={8} style={{ minWidth: 0 }}>
          {entry.kind === 'dir' ? (
            <FolderOutlined style={{ color: token.colorTextTertiary }} />
          ) : entry.kind === 'symlink' ? (
            <LinkOutlined style={{ color: token.colorWarning }} />
          ) : (
            <FileOutlined style={{ color: token.colorTextTertiary }} />
          )}
          <Text style={entry.kind === 'symlink' ? { fontFamily: CODE_FONT_STACK } : undefined}>{entry.name}</Text>
          {entry.kind === 'symlink' && (
            <>
              <Text type="secondary">→</Text>
              <Text code style={{ fontFamily: CODE_FONT_STACK }}>
                {entry.linkTarget}
              </Text>
              {entry.linkEscapes && (
                <Tooltip title="The target is outside this Volume. os.Root refuses to open it, so the Explorer does too — the link is shown rather than hidden, because it is part of the data.">
                  <Tag color="warning">outside the Volume</Tag>
                </Tooltip>
              )}
            </>
          )}
        </Flex>
      ),
      title: 'Name',
    },
    {
      dataIndex: 'size',
      render: (size: number, entry) => (entry.kind === 'dir' ? <Text type="secondary">—</Text> : formatBytes(size)),
      title: 'Size',
      width: 110,
    },
    {
      dataIndex: 'uid',
      render: (uid: number, entry) => (
        <Text type="secondary">{formatOwner(uid, entry.gid)}</Text>
      ),
      title: 'Owner',
      width: 110,
    },
    {
      dataIndex: 'mode',
      render: (mode: string) => <Text code>{mode}</Text>,
      title: 'Mode',
      width: 90,
    },
    {
      dataIndex: 'modifiedAt',
      render: (value: string) => <RelativeTime value={value} />,
      title: 'Modified',
      width: 150,
    },
    {
      key: 'actions',
      render: (_: unknown, entry) => (
        <Flex gap={4} justify="flex-end">
          {entry.kind === 'file' && (
            <Button icon={<EditOutlined />} onClick={() => openFile(entry)} size="small" type="text" />
          )}
          <Dropdown
            menu={{
              items: [
                { disabled: entry.kind === 'dir', icon: <DownloadOutlined />, key: 'download', label: 'Download' },
                { disabled: !writable, icon: <EditOutlined />, key: 'rename', label: 'Rename' },
                { type: 'divider' },
                { danger: true, disabled: !writable, icon: <DeleteOutlined />, key: 'delete', label: 'Delete' },
              ],
              onClick: ({ key }) => {
                if (key === 'download') message.warning(`Downloading ${entry.name}`);
                if (key === 'rename') message.warning('Rename is out of scope for the design pass');
              },
            }}
            trigger={['click']}
          >
            <Button icon={<MoreOutlined />} size="small" type="text" />
          </Dropdown>
          <Popconfirm
            cancelText="Keep it"
            description={
              <>
                <Text style={{ fontFamily: CODE_FONT_STACK }}>{entry.name}</Text>
                {entry.kind === 'dir' ? ' and everything under it' : ''} is removed from the Volume. This is not a trash
                — there is no undo.
              </>
            }
            okButtonProps={{ danger: true }}
            okText="Delete"
            title="Delete permanently?"
          >
            <Button danger disabled={!writable} icon={<DeleteOutlined />} size="small" type="text" />
          </Popconfirm>
        </Flex>
      ),
      title: '',
      width: 130,
    },
  ];
}

/**
 * Downloading a directory as an archive — and saying what is in it first.
 *
 * The symlink count is not a nicety. Because the panel walks the tree anyway, it
 * knows before the operator downloads whether the archive holds a link pointing
 * outside the Volume, and that is the one fact that decides whether extracting it
 * somewhere else is safe. Cheaper to say now than to explain later.
 */
function DownloadDialog({
  onClose,
  open,
  path,
  rootName,
  volumeId,
}: {
  onClose: () => void;
  open: boolean;
  path: string;
  rootName: string;
  volumeId: string;
}) {
  const { message } = App.useApp();
  const sceneContainer = useSceneContainer();
  const [format, setFormat] = useState<'tar.gz' | 'zip'>('tar.gz');
  const summary = useMemo(() => summarizeArchive(volumeId, path), [path, volumeId]);
  const crumbs = path.split('/').filter(Boolean);
  const target = crumbs.length === 0 ? rootName : crumbs[crumbs.length - 1];

  return (
    <Modal
      cancelText="Cancel"
      getContainer={sceneContainer}
      okText={`Download ${format}`}
      onCancel={onClose}
      onOk={() => {
        onClose();
        message.success(`Downloading ${target}.${format}`);
      }}
      open={open}
      title="Download as an archive"
    >
      <Radio.Group
        onChange={(event) => setFormat(event.target.value as 'tar.gz' | 'zip')}
        optionType="button"
        options={[
          { label: 'tar.gz', value: 'tar.gz' },
          { label: 'zip', value: 'zip' },
        ]}
        value={format}
      />
      <Descriptions
        column={1}
        items={[
          {
            children: (
              <Text style={{ fontFamily: CODE_FONT_STACK }}>
                {target}.{format}
              </Text>
            ),
            key: 'name',
            label: 'File',
          },
          { children: `${summary.files} files, ${formatBytes(summary.bytes)}`, key: 'size', label: 'Contents' },
        ]}
        labelStyle={{ width: 120 }}
        size="small"
        style={{ marginTop: 16 }}
      />
      {summary.symlinks > 0 && (
        <Alert
          description={
            <>
              {summary.symlinks === 1 ? 'One entry is' : `${summary.symlinks} entries are`} a symlink, and{' '}
              {summary.escapingSymlinks === 1 ? 'one of them points' : `${summary.escapingSymlinks} of them point`} outside
              this Volume. They are stored as symlinks, because an archive that quietly dropped them would not be a copy
              of your data. Extract it somewhere you trust.
            </>
          }
          message="This archive contains symlinks"
          showIcon
          style={{ marginTop: 16 }}
          type="warning"
        />
      )}
      <Text type="secondary" style={{ display: 'block', marginTop: 16 }}>
        Streamed straight through, with nothing staged on the panel's disk.
      </Text>
    </Modal>
  );
}

function joinPath(parent: string, name: string): string {
  return [parent.replace(/\/+$/, ''), name].filter(Boolean).join('/');
}
