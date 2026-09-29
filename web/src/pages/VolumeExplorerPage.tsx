import {
  ArrowLeftOutlined,
  CloudDownloadOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  FileOutlined,
  FolderOutlined,
  ImportOutlined,
  LinkOutlined,
  LockOutlined,
  ReloadOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import {
  Alert,
  App,
  Button,
  Card,
  Flex,
  Modal,
  Popconfirm,
  Result,
  Skeleton,
  Spin,
  Table,
  Tag,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router';

import { paths } from '../app/paths';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { CodeValue, EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { VolumeFileEditor } from '../components/VolumeFileEditor';
import { resolvedVolumePath, volumeLabel } from '../components/volumes-view';
import { volumeArchiveUrl, volumeReadUrl } from '../data/api';
import { usePanel } from '../data/store';
import type { ComposeVolumes, VolumeEntry } from '../data/types';
import { findComposeLocation } from '../data/types';
import { composeLinkIsStale } from './compose-view';
import type { EntryFacts, RestoreDisclosure, VolumeFailure } from './volume-explorer-view';
import {
  deleteFailure,
  editFailure,
  editorContent,
  editorGate,
  entryFacts,
  explorerView,
  isEscapeRefusal,
  isNotDirectoryRefusal,
  isSymlink,
  joinPath,
  openFailure,
  operationRefusal,
  parentPath,
  pathSegments,
  requiresConfirmation,
  restoreFailure,
  restoreIntent,
  restoreTarget,
  runningWriteWarning,
  uploadFailure,
  uploadTooLarge,
} from './volume-explorer-view';

const { Paragraph, Text } = Typography;

type Row = { entry: VolumeEntry; facts: EntryFacts };

/**
 * The Volume Explorer (GUIDELINES §4.7): a Compose's Volume opened as its own
 * route in its own browser tab, listing one directory at a time, downloading a
 * file or the current directory as a tar.gz, and — ticket 08's write slice —
 * uploading a file into the current directory, editing a text file in Monaco,
 * and deleting a file or a whole directory tree. Ticket 12 adds the restore:
 * picking a tar.gz and streaming it into the current directory, one operation
 * that extracts as it uploads and stores the archive nowhere.
 *
 * The editor is the full screen treatment the Compose YAML screen uses
 * (`VolumeFileEditor`, following `FullscreenEditor`). Opening is an explicit
 * per-row action, never the row click's meaning: a file above the 1 MB editor
 * cap is refused before it is fetched, fetched bytes are refused when they are
 * binary (see `editorContent`), and a save is one PUT that updates the row in
 * place — no re-read of the file or the directory.
 *
 * The screen states its own identity (the Volume's label, its resolved host
 * path, and every Service's container-side target), because a Volume is
 * meaningless without its Compose as context. The file list runs long and
 * the page scrolls as one document; nothing here is a nested scroller.
 *
 * Two pre-flight outcomes decide what the screen refuses:
 *
 *  - A source the pre-flight marks `missing` is refused with the API's own
 *    409 wording, rather than recreated by a mount. It refuses the writes too,
 *    because a write mounts the same source.
 *  - A source the panel cannot check — an absolute bind outside its data
 *    directory — waits for one acknowledgement before the first read, because
 *    mounting it is what would create it as `root:root 0755`.
 *
 * Every write is a PUT or a DELETE, never a GET, so the `SameSite=Lax`
 * session cookie keeps them CSRF-safe. A delete is permanent — there is no
 * trash, because a hidden one inside the Volume would be visible to the
 * application using it — so it is confirmed by a `Popconfirm` naming the path.
 * A Compose that is running raises a banner naming the consequence and the
 * Services using this Volume; the write is deliberately not gated behind it.
 */
export function VolumeExplorerPage() {
  const { composeId: composeParam, environmentId: environmentParam, projectId: projectParam } = useParams();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const {
    deleteVolumeEntry,
    isPending,
    listVolumeEntries,
    loadVolumes,
    projects,
    readVolumeFile,
    restoreVolume,
    writeVolumeFile,
  } = usePanel();

  const projectId = Number(projectParam);
  const environmentId = Number(environmentParam);
  const composeId = Number(composeParam);
  const source = searchParams.get('source') ?? '';
  const path = searchParams.get('path') ?? '';
  const composePath = { composeId, environmentId, projectId };

  // ---- The tree, and the one resolution fetch ------------------------------

  const composeLocation = findComposeLocation(projects, composeId);
  const compose = composeLocation?.compose;
  const environment = composeLocation?.environment;
  const project = composeLocation?.project;
  const knownComposeId = compose?.id;

  const [resolution, setResolution] = useState<{ composeId: number; payload?: ComposeVolumes; error?: string }>();
  const resolutionRequest = useRef(0);
  const fetchResolution = useCallback(() => {
    const request = ++resolutionRequest.current;
    setResolution({ composeId });
    return loadVolumes(composeId)
      .then((payload) => {
        if (resolutionRequest.current === request) {
          setResolution({ composeId, payload });
        }
      })
      .catch((reason: unknown) => {
        if (resolutionRequest.current === request) {
          setResolution({ composeId, error: failureMessage(reason) });
        }
      });
  }, [composeId, loadVolumes]);

  useEffect(() => {
    if (knownComposeId !== undefined) {
      void fetchResolution();
    }
  }, [fetchResolution, knownComposeId]);

  // A link whose ids do not match where the Compose lives is sent to the
  // canonical one, the same rule the Compose screen applies — the source and
  // the relative path ride along untouched.
  const linkIsStale = composeLinkIsStale(composeLocation, projectId, environmentId);
  const canonicalPath =
    compose && environment && project
      ? paths.volumeExplorer(project.id, environment.id, compose.id, source, path)
      : undefined;
  useEffect(() => {
    if (linkIsStale && canonicalPath) {
      navigate(canonicalPath, { replace: true });
    }
  }, [canonicalPath, linkIsStale, navigate]);

  const resolved = resolution?.composeId === composeId ? resolution : undefined;
  const view = explorerView(resolved?.payload, resolved?.error, source);

  // ---- The unverifiable gate -----------------------------------------------
  //
  // One acknowledgement per Explorer visit, before any list or read runs: the
  // panel cannot tell whether an absolute bind outside its data directory
  // exists, and mounting it is what would create it. A decline pauses reads
  // rather than dismissing the screen.

  const [gate, setGate] = useState<'pending' | 'accepted' | 'declined'>('pending');
  const confirmationRequired = view.status === 'ready' && requiresConfirmation(view.volume);
  const gated = confirmationRequired ? gate === 'accepted' : true;

  // ---- The listing ----------------------------------------------------------

  const listingKey = `${source}\u0000${path}`;
  const listPendingKey = `volume-list:${composeId}:${source}:${path}`;
  const [listing, setListing] = useState<{ key: string; entries?: VolumeEntry[]; error?: string }>();
  const [selected, setSelected] = useState<string>();
  const [openError, setOpenError] = useState<{ entry: VolumeEntry; error: string }>();
  // A refused or failed write stays on this screen as an Alert (GUIDELINES §8),
  // carrying the API's words with the limit that was hit named.
  const [writeError, setWriteError] = useState<VolumeFailure>();
  // The editor: the one open file, the entry-level refusals (too large,
  // binary) shown above the list, and the last save's failure shown inside
  // the editor. `original` is the file as it was opened or last saved; the
  // draft is the page's, handed to the editor on every change.
  const [editing, setEditing] = useState<{ draft: string; original: string; path: string }>();
  const [editorRefusal, setEditorRefusal] = useState<{ failure: VolumeFailure; path: string }>();
  const [saveError, setSaveError] = useState<VolumeFailure>();
  const [uploading, setUploading] = useState(false);
  // The restore: the picked archive waits behind a confirmation carrying the
  // whole disclosure, and only a confirm runs it. Cancelling drops the file
  // and sends nothing.
  const [restoring, setRestoring] = useState(false);
  const [restore, setRestore] = useState<{ disclosure: RestoreDisclosure; file: File }>();
  const fileInput = useRef<HTMLInputElement>(null);
  const restoreInput = useRef<HTMLInputElement>(null);
  const listRequest = useRef(0);

  const loadList = useCallback(async () => {
    const request = ++listRequest.current;
    const key = listingKey;
    try {
      const entries = await listVolumeEntries(composeId, source, path);
      if (listRequest.current === request) {
        setListing({ key, entries });
      }
    } catch (reason: unknown) {
      if (listRequest.current === request) {
        setListing({ key, error: failureMessage(reason) });
      }
    }
  }, [composeId, listVolumeEntries, listingKey, path, source]);

  const currentListing = listing?.key === listingKey ? listing : undefined;
  const canList = view.status === 'ready' && gated;

  useEffect(() => {
    if (canList && currentListing === undefined) {
      void loadList();
    }
  }, [canList, currentListing, loadList]);

  // The selection and any open failure belong to the directory they were made
  // in; navigating starts clean, and so does a write failure.
  useEffect(() => {
    setSelected(undefined);
    setOpenError(undefined);
    setWriteError(undefined);
    setEditorRefusal(undefined);
    setSaveError(undefined);
    // A picked archive belongs to the directory it was picked in; a
    // navigation (browser back included) drops it unconfirmed.
    setRestore(undefined);
  }, [listingKey]);

  const rows = useMemo<Row[]>(
    () => (currentListing?.entries ?? []).map((entry) => ({ entry, facts: entryFacts(entry, path) })),
    [currentListing, path],
  );
  // A file, or a symlink the probe resolved to a file: both download through
  // `read`, which follows an inside-the-root link.
  const selectedRow = rows.find(
    (row) => row.entry.name === selected && (row.entry.kind === 'file' || row.entry.kind === 'symlink'),
  );
  const refusal = view.status === 'ready' || view.status === 'refused' ? operationRefusal(view.volume) : undefined;
  const failure = openError ? openFailure(openError.entry, openError.error) : undefined;

  const goTo = (next: string) => navigate(paths.volumeExplorer(projectId, environmentId, composeId, source, next));
  const backToVolumes = () => navigate(paths.compose(projectId, environmentId, composeId, 'volumes'));

  // A directory navigates straight away — its listing's failure is the
  // screen's read error, with Retry. A symlink is probed by listing it as a
  // directory: the root either resolves it (its listing loads and the screen
  // follows it), answers "not a directory" (it is an entry to download, so it
  // is selected), or refuses it (the API's own words are shown). A file is
  // selected, which is what the toolbar downloads.
  const openEntry = useCallback(
    async (entry: VolumeEntry) => {
      if (entry.kind === 'file') {
        setSelected(entry.name);
        return;
      }
      if (entry.kind !== 'directory' && entry.kind !== 'symlink') {
        return;
      }
      setOpenError(undefined);
      const next = joinPath(path, entry.name);
      if (entry.kind === 'directory') {
        navigate(paths.volumeExplorer(projectId, environmentId, composeId, source, next));
        return;
      }
      try {
        const entries = await listVolumeEntries(composeId, source, next);
        // The listing is already in hand: stash it so following the symlink
        // does not pay for the same read twice.
        listRequest.current += 1;
        setListing({ key: `${source}\u0000${next}`, entries });
        navigate(paths.volumeExplorer(projectId, environmentId, composeId, source, next));
      } catch (reason: unknown) {
        const refusal = failureMessage(reason);
        if (isNotDirectoryRefusal(refusal)) {
          setSelected(entry.name);
          return;
        }
        setOpenError({ entry, error: refusal });
      }
    },
    [composeId, environmentId, listVolumeEntries, navigate, path, projectId, source],
  );

  // ---- The write actions -----------------------------------------------------
  //
  // Upload and delete both target the current directory (`path`), both
  // refresh the listing on success rather than patching it — the Volume is
  // the server's — and both keep a failure on screen with the API's own
  // words. The editor's save is the third: it patches its one row instead,
  // because a save re-reads nothing.

  const upload = useCallback(
    async (file: File) => {
      setWriteError(undefined);
      // The 1 GiB cap's client-side fast path: refusing an oversize file here
      // saves a request, but the API's 413 is the check that counts.
      const refused = uploadTooLarge(file);
      if (refused) {
        setWriteError(refused);
        return;
      }
      const target = joinPath(path, file.name);
      setUploading(true);
      try {
        await writeVolumeFile(composeId, source, target, file);
        message.success(`Uploaded ${target}`);
        await loadList();
      } catch (reason: unknown) {
        setWriteError(uploadFailure(reason));
      } finally {
        setUploading(false);
      }
    },
    [composeId, loadList, message, path, source, writeVolumeFile],
  );

  // ---- The restore actions -----------------------------------------------------
  //
  // Picking an archive decides, before anything is sent, whether it earns the
  // confirmation — which carries the whole disclosure, target named — or a
  // refusal from the client-side compressed-cap fast path. Only a confirm runs
  // the PUT; cancelling the modal drops the file. The extract is one held-open
  // request, so the toolbar's action spins and the listing refreshes when it
  // lands.

  const pickRestore = useCallback(
    (file: File) => {
      setWriteError(undefined);
      const intent = restoreIntent(file, path);
      if (intent.status === 'refused') {
        setWriteError(intent.failure);
        return;
      }
      setRestore({ disclosure: intent.disclosure, file });
    },
    [path],
  );

  const runRestore = useCallback(async () => {
    const picked = restore;
    if (picked === undefined) {
      return;
    }
    setRestore(undefined);
    setWriteError(undefined);
    setRestoring(true);
    try {
      await restoreVolume(composeId, source, path, picked.file);
      message.success(`Extracted ${picked.file.name} into ${restoreTarget(path)}`);
      await loadList();
    } catch (reason: unknown) {
      setWriteError(restoreFailure(reason));
    } finally {
      setRestoring(false);
    }
  }, [composeId, loadList, message, path, restore, restoreVolume, source]);

  const removeEntry = useCallback(
    async (entry: VolumeEntry) => {
      setWriteError(undefined);
      const target = joinPath(path, entry.name);
      try {
        await deleteVolumeEntry(composeId, source, target);
        if (selected === entry.name) {
          setSelected(undefined);
        }
        message.success(`Deleted ${target}`);
        await loadList();
      } catch (reason: unknown) {
        setWriteError(deleteFailure(reason));
      }
    },
    [composeId, deleteVolumeEntry, loadList, message, path, selected, source],
  );

  // ---- The editor ------------------------------------------------------------
  //
  // Opening is the row action's meaning, never the row click's; the 1 MB gate
  // runs before the fetch, and the fetched bytes are decoded by the view
  // helper's binary rule. Any read failure keeps the screen's established
  // presentation (the API's own words in the open alert).

  const editEntry = useCallback(
    async (entry: VolumeEntry) => {
      setOpenError(undefined);
      setEditorRefusal(undefined);
      setSaveError(undefined);
      const target = joinPath(path, entry.name);
      const tooLarge = editorGate(entry, target);
      if (tooLarge !== undefined) {
        setEditorRefusal({ failure: tooLarge, path: target });
        return;
      }
      try {
        const content = editorContent(target, await readVolumeFile(composeId, source, target));
        if (content.status === 'refused') {
          setEditorRefusal({ failure: content.failure, path: target });
          return;
        }
        setEditing({ draft: content.text, original: content.text, path: target });
      } catch (reason: unknown) {
        setOpenError({ entry, error: failureMessage(reason) });
      }
    },
    [composeId, path, readVolumeFile, source],
  );

  // A save is one PUT. On success the draft becomes what the file was saved
  // as — so nothing is re-read — and the listing's row takes the new size and
  // time in place, which keeps the screen in sync without a second operation.
  const saveFile = useCallback(async () => {
    if (editing === undefined) {
      return;
    }
    setSaveError(undefined);
    try {
      await writeVolumeFile(
        composeId,
        source,
        editing.path,
        new Blob([editing.draft], { type: 'text/plain;charset=utf-8' }),
      );
      const size = new TextEncoder().encode(editing.draft).length;
      const name = editing.path.slice(editing.path.lastIndexOf('/') + 1);
      setEditing((current) => (current === undefined ? current : { ...current, original: current.draft }));
      setListing((current) => {
        if (current === undefined || current.key !== listingKey || current.entries === undefined) {
          return current;
        }
        return {
          ...current,
          entries: current.entries.map((entry) =>
            entry.name === name ? { ...entry, mtime: new Date().toISOString(), size } : entry,
          ),
        };
      });
      message.success(`Saved ${editing.path}`);
    } catch (reason: unknown) {
      setSaveError(editFailure(reason));
    }
  }, [composeId, editing, listingKey, message, source, writeVolumeFile]);

  if (!compose || !environment || !project) {
    // A new tab can land before the Project tree has loaded; a loading card
    // covers that window, the design's not-found covers a stale link.
    if (isPending('projects') && projects.length === 0) {
      return (
        <Card variant="outlined">
          <Flex justify="center" style={{ padding: '48px 0' }}>
            <Spin size="large" />
          </Flex>
        </Card>
      );
    }
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

  const volume = view.status === 'ready' ? view.volume : view.status === 'refused' ? view.volume : undefined;

  // ---- The toolbar -----------------------------------------------------------
  //
  // Upload and restore are the toolbar's write controls; editing and deletion
  // belong to the row whose file they touch. The download actions stay anchors
  // or a re-read, and a missing source disables everything with the API's own
  // reason rather than letting a mount recreate it. A write is a PUT or a
  // DELETE, never a GET, so the session cookie keeps it CSRF-safe.

  const toolbar = (
    <Flex gap={8} wrap>
      <Tooltip
        title={
          refusal ??
          'Uploads a file into this directory. The 1 GiB cap is checked in the browser; the Volume’s free space is checked before anything is written.'
        }
      >
        <Button
          disabled={refusal !== undefined}
          icon={<UploadOutlined />}
          loading={uploading}
          onClick={() => fileInput.current?.click()}
        >
          Upload
        </Button>
      </Tooltip>
      <Tooltip
        title={
          refusal ??
          'Extracts a tar.gz into this directory. Upload and extract are one operation, the archive is never stored, and a stream that fails part-way leaves the entries it already wrote.'
        }
      >
        <Button
          disabled={refusal !== undefined}
          icon={<ImportOutlined />}
          loading={restoring}
          onClick={() => restoreInput.current?.click()}
        >
          Restore tar.gz
        </Button>
      </Tooltip>
      <Tooltip
        title={
          refusal ??
          (selectedRow
            ? `Downloads ${selectedRow.entry.name} straight to disk.`
            : 'Select a file to download — a directory downloads as a tar.gz archive.')
        }
      >
        <Button
          disabled={selectedRow === undefined || refusal !== undefined}
          href={
            selectedRow && refusal === undefined
              ? volumeReadUrl(composePath, source, joinPath(path, selectedRow.entry.name))
              : undefined
          }
          icon={<DownloadOutlined />}
        >
          Download file
        </Button>
      </Tooltip>
      <Tooltip title={refusal ?? 'Downloads this directory as a tar.gz archive, straight to disk.'}>
        <Button
          disabled={refusal !== undefined}
          href={refusal === undefined ? volumeArchiveUrl(composePath, source, path) : undefined}
          icon={<CloudDownloadOutlined />}
        >
          Download tar.gz
        </Button>
      </Tooltip>
      <Tooltip title={refusal ?? 'Re-read this directory'}>
        <Button
          aria-label="Refresh directory"
          disabled={refusal !== undefined}
          icon={<ReloadOutlined />}
          loading={isPending(listPendingKey)}
          onClick={() => void loadList()}
        />
      </Tooltip>
    </Flex>
  );

  const header = (
    <PageHeader
      description={
        <Flex align="center" gap={6} wrap>
          <span>
            {project.name} / {environment.name} / {compose.name}
          </span>
          <span>·</span>
          <span>Files in this Volume</span>
          {volume && (
            <>
              <span>·</span>
              <CodeValue copyable label="source:" value={resolvedVolumePath(volume)} />
            </>
          )}
        </Flex>
      }
      tags={volume ? <Tag color={volume.kind === 'bind' ? 'geekblue' : undefined}>{volume.kind}</Tag> : undefined}
      title={volume ? volumeLabel(volume) : source || 'Volume Explorer'}
      titleExtra={
        volume && (
          <Flex align="center" gap={8} wrap>
            <Text type="secondary">Mounted by</Text>
            {volume.uses.map((use) => (
              <Flex align="center" gap={6} key={`${use.service}:${use.target}`}>
                <Tag style={{ marginInlineEnd: 0 }}>{use.service}</Tag>
                <Text type="secondary">→</Text>
                <Text code style={{ fontSize: 12 }}>
                  {use.target}
                </Text>
                {use.readOnly && (
                  <Tooltip title="This Service mounts the Volume read-only.">
                    <Tag icon={<LockOutlined />}>ro</Tag>
                  </Tooltip>
                )}
              </Flex>
            ))}
          </Flex>
        )
      }
    />
  );

  const columns: ColumnsType<Row> = [
    {
      key: 'name',
      render: (_value, row) => (
        <Flex align="center" gap={8} wrap>
          {row.entry.kind === 'directory' ? (
            <FolderOutlined style={{ color: token.colorTextTertiary }} />
          ) : row.entry.kind === 'symlink' ? (
            <LinkOutlined style={{ color: token.colorWarning }} />
          ) : (
            <FileOutlined style={{ color: token.colorTextTertiary }} />
          )}
          <Text style={{ fontFamily: CODE_FONT_STACK, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
            {row.facts.name}
          </Text>
          {row.facts.target !== undefined && (
            <>
              <Text type="secondary">→</Text>
              <Text code style={{ fontFamily: CODE_FONT_STACK }}>
                {row.facts.target}
              </Text>
              {row.facts.escapes && (
                <Tooltip title="The target is outside this Volume. The Volume root refuses to resolve it, so the Explorer does too — the link is shown rather than hidden, because it is part of the data.">
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
      key: 'size',
      render: (_value, row) => (
        <Text type={row.entry.kind === 'directory' ? 'secondary' : undefined}>{row.facts.size}</Text>
      ),
      title: 'Size',
      width: 110,
    },
    {
      key: 'mtime',
      render: (_value, row) => <RelativeTime value={row.facts.modified} />,
      title: 'Modified',
      width: 150,
    },
    {
      key: 'owner',
      render: (_value, row) => <Text type="secondary">{row.facts.owner}</Text>,
      title: 'Owner',
      width: 110,
    },
    {
      key: 'mode',
      render: (_value, row) => <Text code>{row.facts.mode}</Text>,
      title: 'Mode',
      width: 90,
    },
    {
      key: 'kind',
      render: (_value, row) => <Tag>{row.facts.kind}</Tag>,
      title: 'Kind',
      width: 110,
    },
    {
      key: 'actions',
      render: (_value, row) => {
        const target = joinPath(path, row.entry.name);
        // A delete is permanent and there is no trash, because a hidden one
        // inside the Volume would be visible to the application using it — so
        // the confirm names the exact path it removes, whole tree included.
        // Edit is an explicit per-row action of its own: the row's click
        // selects a file for download, and it must never silently edit.
        return (
          // The row itself navigates on click; the action column must not
          // bubble, or opening a directory would be what a delete click means.
          <Flex align="center" gap={4} justify="flex-end" onClick={(event) => event.stopPropagation()}>
            {row.entry.kind === 'file' && (
              <Tooltip title={refusal ?? 'Open this file in the editor'}>
                <Button
                  aria-label={`Edit ${target}`}
                  disabled={refusal !== undefined}
                  icon={<EditOutlined />}
                  loading={isPending(`volume-read:${composeId}:${source}:${target}`)}
                  onClick={() => void editEntry(row.entry)}
                  size="small"
                  type="text"
                />
              </Tooltip>
            )}
            <Popconfirm
              cancelText="Keep it"
              description={
                <>
                  <Text style={{ fontFamily: CODE_FONT_STACK, wordBreak: 'break-all' }}>{target}</Text>
                  {row.entry.kind === 'directory' ? ' and everything under it' : ''} is removed from the Volume.
                  There is no trash — this cannot be undone.
                </>
              }
              okButtonProps={{ danger: true, loading: isPending(`volume-delete:${composeId}:${source}:${target}`) }}
              okText="Delete"
              onConfirm={() => void removeEntry(row.entry)}
              title="Delete permanently?"
            >
              <Button aria-label={`Delete ${target}`} danger icon={<DeleteOutlined />} size="small" type="text" />
            </Popconfirm>
          </Flex>
        );
      },
      title: '',
      width: 88,
    },
  ];

  // ---- The screen's states ---------------------------------------------------

  let body: ReactNode;
  if (view.status === 'loading') {
    // The header keeps the URL's source so the screen does not jump when the
    // Volume's label lands.
    body = <Skeleton active paragraph={{ rows: 6 }} title={false} />;
  } else if (view.status === 'not-deployed') {
    body = (
      <Card variant="outlined">
        <Result
          extra={
            <Button onClick={backToVolumes} type="primary">
              Back to Volumes
            </Button>
          }
          status="warning"
          subTitle="Opening a Volume goes through the same docker compose config resolution as the Volumes tab. That needs the environment file a deploy writes."
          title="This Compose’s Volumes are not resolvable yet"
        />
        <CodeValue label="docker compose:" value={view.message} />
      </Card>
    );
  } else if (view.status === 'unavailable') {
    body = (
      <Card variant="outlined">
        <Result
          extra={
            <Button onClick={backToVolumes} type="primary">
              Back to Volumes
            </Button>
          }
          status="warning"
          subTitle={view.reason}
          title="Volume files are unavailable"
        />
      </Card>
    );
  } else if (view.status === 'resolution-error') {
    body = (
      <Alert
        action={<Button onClick={() => void fetchResolution()}>Retry</Button>}
        description={view.message}
        message="Could not resolve this Compose’s Volumes"
        showIcon
        type="error"
      />
    );
  } else if (view.status === 'not-found') {
    body = (
      <Result
        extra={
          <Button onClick={backToVolumes} type="primary">
            Back to Volumes
          </Button>
        }
        status="404"
        subTitle="It may have been removed from the Compose since this link was made."
        title="Volume not found"
      />
    );
  } else if (view.status === 'refused') {
    body = (
      <Card
        extra={toolbar}
        title={<Crumbs onGo={goTo} path={path} root={volumeLabel(view.volume)} />}
        variant="outlined"
      >
        <Alert description={view.message} message="This Volume’s source is missing" showIcon type="error" />
      </Card>
    );
  } else if (view.status === 'ready' && confirmationRequired && gate === 'declined') {
    body = (
      <Card title={<Crumbs onGo={goTo} path={path} root={volumeLabel(view.volume)} />} variant="outlined">
        <Alert
          action={
            <Button onClick={() => setGate('pending')} type="primary">
              Continue
            </Button>
          }
          description={view.volume.preflight.warning}
          message="Reads and writes are paused until you confirm"
          showIcon
          type="warning"
        />
      </Card>
    );
  } else {
    // Any Service up raises the banner, and it names the Services that mount
    // this Volume. It is deliberately not a gate (GUIDELINES §8): the write
    // proceeds and the banner is the whole warning.
    const runningWarning = runningWriteWarning(compose.status, view.volume);
    body = (
      <>
        {runningWarning && (
          <Alert
            description={runningWarning.description}
            message={runningWarning.message}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="warning"
          />
        )}
        {failure && (
          <Alert
            afterClose={() => setOpenError(undefined)}
            closable
            description={failure.description}
            message={failure.message}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type={openError && isSymlink(openError.entry) && isEscapeRefusal(openError.error) ? 'warning' : 'error'}
          />
        )}
        {editorRefusal && (
          <Alert
            action={
              <Button href={volumeReadUrl(composePath, source, editorRefusal.path)} size="small">
                Download
              </Button>
            }
            afterClose={() => setEditorRefusal(undefined)}
            closable
            description={editorRefusal.failure.description}
            message={editorRefusal.failure.message}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="info"
          />
        )}
        {writeError && (
          <Alert
            afterClose={() => setWriteError(undefined)}
            closable
            description={writeError.description}
            message={writeError.message}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="error"
          />
        )}
        <input
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file) void upload(file);
            // The same file can be picked again after a refusal.
            event.target.value = '';
          }}
          ref={fileInput}
          type="file"
        />
        <input
          accept=".tar.gz,.tgz"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file) pickRestore(file);
            // The same file can be picked again after a cancel or a refusal.
            event.target.value = '';
          }}
          ref={restoreInput}
          type="file"
        />
        <Card
          extra={gated ? toolbar : undefined}
          title={<Crumbs onGo={goTo} path={path} root={volumeLabel(view.volume)} />}
          variant="outlined"
        >
          {!gated ? (
            <Skeleton active paragraph={{ rows: 6 }} title={false} />
          ) : currentListing?.error !== undefined ? (
            <Alert
              action={<Button onClick={() => void loadList()}>Retry</Button>}
              description={currentListing.error}
              message="Could not read this directory"
              showIcon
              type="error"
            />
          ) : currentListing === undefined ? (
            <Skeleton active paragraph={{ rows: 6 }} title={false} />
          ) : rows.length === 0 && !isPending(listPendingKey) ? (
            <EmptyState
              description="Nothing is stored here yet. A download of this directory would produce an empty archive."
              title="This directory is empty"
            />
          ) : (
            <Table<Row>
              columns={columns}
              dataSource={rows}
              loading={isPending(listPendingKey)}
              onRow={(row) => ({
                onClick: () => void openEntry(row.entry),
                style:
                  row.entry.kind === 'file' || row.entry.kind === 'directory' || row.entry.kind === 'symlink'
                    ? { cursor: 'pointer' }
                    : undefined,
              })}
              pagination={false}
              rowKey={(row) => row.entry.name}
              rowSelection={{
                getCheckboxProps: (row) => ({ disabled: row.entry.kind !== 'file' }),
                onChange: (keys) => setSelected(keys.length > 0 ? String(keys[0]) : undefined),
                selectedRowKeys: selected ? [selected] : [],
                type: 'radio',
              }}
              size="middle"
            />
          )}
          <Paragraph style={{ marginBottom: 0, marginTop: 12 }} type="secondary">
            Uploads land in this directory and deletes are permanent — there is no trash. A file opens in the editor
            through its own Edit action; files past the editor’s 1 MB cap and binary files are refused there rather
            than opened. Paths come from this Compose’s own resolution, so the panel never accepts a host path from a
            form, and downloads stream straight to disk.
          </Paragraph>
        </Card>
      </>
    );
  }

  return (
    <>
      {header}
      {body}

      <Modal
        cancelText="Cancel"
        okText="Continue"
        onCancel={() => setGate('declined')}
        onOk={() => setGate('accepted')}
        open={confirmationRequired && gate === 'pending'}
        title="Open this Volume?"
      >
        {view.status === 'ready' && (
          <>
            <Paragraph>
              <Text code>{view.volume.source}</Text> is a bind mount outside the panel’s data directory, so the panel
              cannot tell whether it exists without mounting it — and mounting it is what would create it.
            </Paragraph>
            <Alert message={view.volume.preflight.warning} showIcon type="warning" />
            <Paragraph style={{ marginBottom: 0, marginTop: LAYOUT.gutterSmall }} type="secondary">
              Continuing mounts this path for every read and every write. If it is missing, Docker creates it as{' '}
              <Text code>root:root 0755</Text>.
            </Paragraph>
          </>
        )}
      </Modal>

      <Modal
        cancelText="Cancel"
        okText="Extract"
        onCancel={() => setRestore(undefined)}
        onOk={() => void runRestore()}
        open={restore !== undefined}
        title={restore?.disclosure.title}
      >
        {restore && (
          <>
            <ul style={{ marginBottom: 0, paddingInlineStart: 20 }}>
              {restore.disclosure.points.map((point) => (
                <li key={point}>{point}</li>
              ))}
            </ul>
            <Paragraph style={{ marginBottom: 0, marginTop: LAYOUT.gutterSmall }} type="secondary">
              Nothing is sent until you confirm; Cancel leaves the Volume untouched.
            </Paragraph>
          </>
        )}
      </Modal>

      <VolumeFileEditor
        draft={editing?.draft ?? ''}
        failure={saveError}
        onChange={(draft) => setEditing((current) => (current === undefined ? current : { ...current, draft }))}
        onClose={() => {
          setEditing(undefined);
          setSaveError(undefined);
        }}
        onDiscard={() => {
          setSaveError(undefined);
          setEditing((current) => (current === undefined ? current : { ...current, draft: current.original }));
        }}
        onSave={() => void saveFile()}
        open={editing !== undefined}
        original={editing?.original ?? ''}
        path={editing?.path ?? ''}
        saving={editing !== undefined && isPending(`volume-write:${composeId}:${source}:${editing.path}`)}
      />
    </>
  );
}

/** The breadcrumb over the relative path, every segment a way back to it. */
function Crumbs({ onGo, path, root }: { onGo: (path: string) => void; path: string; root: string }) {
  const crumbs = pathSegments(path);
  const at = (index: number) => crumbs.slice(0, index + 1).join('/');
  return (
    <Flex align="center" gap={8} wrap>
      <Button
        aria-label="Up one directory"
        disabled={crumbs.length === 0}
        icon={<ArrowLeftOutlined />}
        onClick={() => onGo(parentPath(path))}
        size="small"
        type="text"
      />
      <Text
        onClick={() => onGo('')}
        strong={crumbs.length === 0}
        style={{ cursor: 'pointer', fontFamily: CODE_FONT_STACK, whiteSpace: 'pre-wrap' }}
      >
        {root}
      </Text>
      {crumbs.map((crumb, index) => (
        <Flex align="center" gap={8} key={`${index}-${crumb}`}>
          <Text type="secondary">/</Text>
          <Text
            onClick={() => onGo(at(index))}
            strong={index === crumbs.length - 1}
            style={{ cursor: 'pointer', fontFamily: CODE_FONT_STACK, whiteSpace: 'pre-wrap' }}
          >
            {crumb}
          </Text>
        </Flex>
      ))}
    </Flex>
  );
}

function failureMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : String(reason);
}
