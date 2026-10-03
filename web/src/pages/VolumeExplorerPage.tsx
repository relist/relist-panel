import {
  ArrowLeftOutlined,
  CloudDownloadOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  FileAddOutlined,
  FileOutlined,
  FolderAddOutlined,
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
  Select,
  Input,
  Checkbox,
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
import { resolvedVolumePath, volumeLabel, volumeAccessReady, volumeIdentityValid, serviceUserRefusal } from '../components/volumes-view';
import { volumeAccessQuery, volumeArchiveUrl, volumeReadUrl } from '../data/api';
import { usePanel } from '../data/store';
import type { ComposeVolumes, VolumeAccess, VolumeEntry } from '../data/types';
import { findComposeLocation } from '../data/types';
import { composeLinkIsStale } from './compose-view';
import type { EntryFacts, NewEntryKind, RestoreDisclosure, VolumeFailure } from './volume-explorer-view';
import {
  savedEditorState,
  editorSessionCurrent,
  editorReadCurrent,
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
  newEntryFailure,
  newEntryNameError,
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

/** What every operation sends until the Operation user card is confirmed: nothing. */
const UNCONFIRMED_ACCESS: VolumeAccess = {};

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
 * The Operation user card is a form with an explicit Confirm: the identity
 * every operation runs as — a mounting Service's resolved `user:`, or a
 * numeric uid:gid override — and the acknowledgements it requires are applied
 * in one step. Nothing is listed, read, downloaded or written until Confirm,
 * so a half-typed uid:gid never reaches the API and a changed selection cannot
 * silently switch the identity a click already meant.
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
 * A new folder or file is one dialog and one helper run, created as the
 * confirmed operation user; `create` is O_EXCL, so a name already taken is
 * refused rather than replaced, unlike an upload's overwriting write.
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
    createVolumeDirectory,
    createVolumeFile,
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

  const [identity, setIdentity] = useState('');
  const [override, setOverride] = useState('');
  const [bindAck, setBindAck] = useState('');
  const [rootAck, setRootAck] = useState('');
  // The card's form is a draft; `applied` is the snapshot Confirm froze. Every
  // operation — list, read, download, upload, restore, delete — runs as the
  // applied identity, so nothing is sent under a half-chosen one and editing
  // the form afterwards cannot silently switch what a click already meant.
  const [applied, setApplied] = useState<{ access: VolumeAccess; query: string }>();
  const identityVolume = view.status === 'ready' || view.status === 'refused' ? view.volume : undefined;
  const mountingUsers = (resolved?.payload?.services ?? []).filter((candidate) =>
    identityVolume?.uses.some((use) => use.service === candidate.service),
  );
  const chosen = mountingUsers.find((candidate) => candidate.service === identity);
  const operationIdentity = identity === '__override' ? override : chosen?.user;
  const identityValid = volumeIdentityValid(operationIdentity);
  const chosenRefusal = serviceUserRefusal(chosen?.user);
  const rootRequired = identityValid && Number(operationIdentity?.split(':')[0]) === 0;
  const riskKey = `${composeId}:${source}:${identityVolume?.preflight.status}:${identityVolume?.preflight.warning}`;
  const identityKey = `${riskKey}:${identity}:${operationIdentity}`;
  const confirmationRequired = view.status === 'ready' && requiresConfirmation(view.volume);
  // Confirm unlocks only for a complete draft: a valid identity carrying every
  // acknowledgement it requires.
  const formReady = volumeAccessReady(operationIdentity, confirmationRequired, {
    bindConfirmed: bindAck === riskKey,
    rootConfirmed: rootAck === identityKey,
  });
  const draftAccess = useMemo<VolumeAccess>(
    () => ({
      ...(identity === '__override' ? { user: override } : { service: identity }),
      bindConfirmed: bindAck === riskKey,
      rootConfirmed: rootAck === identityKey,
    }),
    [identity, override, bindAck, rootAck, riskKey, identityKey],
  );
  const draftQuery = volumeAccessQuery(draftAccess);
  const appliedIsCurrent = applied !== undefined && applied.query === draftQuery;
  const access = applied?.access ?? UNCONFIRMED_ACCESS;
  const confirmed = applied !== undefined;
  const confirmedLabel = applied?.access.user ?? applied?.access.service;
  // The uid:gid the create dialog states as the new entry's owner: the
  // override itself, or the confirmed Service's resolved user. Undefined only
  // in the instant before a service's resolution is in hand.
  const confirmedUser =
    applied?.access.user ?? mountingUsers.find((user) => user.service === applied?.access.service)?.user;
  const confirmOperationUser = useCallback(() => {
    setApplied({ access: draftAccess, query: draftQuery });
  }, [draftAccess, draftQuery]);
  useEffect(() => {
    setIdentity('');
    setOverride('');
    setBindAck('');
    setRootAck('');
    setApplied(undefined);
    setEditing(undefined);
  }, [composeId, source]);

  // ---- The listing ----------------------------------------------------------

  // The listing belongs to the confirmed identity, not the card's draft: a
  // changed draft does not drop or refetch the directory until Confirm.
  const confirmedQuery = applied?.query ?? '';
  const listingKey = `${confirmedQuery}\u0000${path}`;
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
  const editorSession = useRef(0);
  const editorContext = listingKey;
  const editorRead = useRef({ request: 0, context: editorContext });
  // Invalidate pending reads immediately when render moves to another context.
  // Incrementing also protects navigation away and back to the same Volume.
  if (editorRead.current.context !== editorContext) {
    editorRead.current = { request: editorRead.current.request + 1, context: editorContext };
  }
  const [editing, setEditing] = useState<{ session: number; draft: string; original: string; path: string }>();
  const currentEditor = useRef(editing);
  currentEditor.current = editing;
  const [editorRefusal, setEditorRefusal] = useState<{ failure: VolumeFailure; path: string }>();
  const [saveError, setSaveError] = useState<VolumeFailure>();
  useEffect(() => {
    setEditing(undefined);
    setEditorRefusal(undefined);
    setSaveError(undefined);
  }, [editorContext]);
  const [uploading, setUploading] = useState(false);
  // The restore: the picked archive waits behind a confirmation carrying the
  // whole disclosure, and only a confirm runs it. Cancelling drops the file
  // and sends nothing.
  const [restoring, setRestoring] = useState(false);
  const [restore, setRestore] = useState<{ disclosure: RestoreDisclosure; file: File }>();
  // The create dialog: one component for New folder and New file, whose name
  // rule is the pure `newEntryNameError`. Nothing is sent until Create;
  // success re-reads the directory, and a failure keeps the screen's alert.
  const [newEntry, setNewEntry] = useState<{ kind: NewEntryKind; name: string }>();
  const [creating, setCreating] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const restoreInput = useRef<HTMLInputElement>(null);
  const listRequest = useRef(0);

  const loadList = useCallback(async () => {
    const request = ++listRequest.current;
    const key = listingKey;
    try {
      const entries = await listVolumeEntries(composeId, source, path, access);
      if (listRequest.current === request) {
        setListing({ key, entries });
      }
    } catch (reason: unknown) {
      if (listRequest.current === request) {
        setListing({ key, error: failureMessage(reason) });
      }
    }
  }, [access, composeId, listVolumeEntries, listingKey, path, source]);

  const currentListing = listing?.key === listingKey ? listing : undefined;
  const canList = view.status === 'ready' && confirmed;

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
        const entries = await listVolumeEntries(composeId, source, next, access);
        // The listing is already in hand: stash it so following the symlink
        // does not pay for the same read twice.
        listRequest.current += 1;
        setListing({ key: `${confirmedQuery}\u0000${next}`, entries });
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
    [access, composeId, confirmedQuery, environmentId, listVolumeEntries, navigate, path, projectId, source],
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
        await writeVolumeFile(composeId, source, target, file, access);
        message.success(`Uploaded ${target}`);
        await loadList();
      } catch (reason: unknown) {
        setWriteError(uploadFailure(reason));
      } finally {
        setUploading(false);
      }
    },
    [access, composeId, loadList, message, path, source, writeVolumeFile],
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
      await restoreVolume(composeId, source, path, picked.file, access);
      message.success(`Extracted ${picked.file.name} into ${restoreTarget(path)}`);
      await loadList();
    } catch (reason: unknown) {
      setWriteError(restoreFailure(reason));
    } finally {
      setRestoring(false);
    }
  }, [access, composeId, loadList, message, path, restore, restoreVolume, source]);

  const removeEntry = useCallback(
    async (entry: VolumeEntry) => {
      setWriteError(undefined);
      const target = joinPath(path, entry.name);
      try {
        await deleteVolumeEntry(composeId, source, target, access);
        if (selected === entry.name) {
          setSelected(undefined);
        }
        message.success(`Deleted ${target}`);
        await loadList();
      } catch (reason: unknown) {
        setWriteError(deleteFailure(reason));
      }
    },
    [access, composeId, deleteVolumeEntry, loadList, message, path, selected, source],
  );

  // ---- The create actions (New folder / New file) ----------------------------
  //
  // The dialog's name rule is the pure `newEntryNameError`: the empty name
  // keeps Create disabled, a duplicate in the listing is refused before a
  // request, and the API's own 409 is what a race still earns. A create runs
  // as the confirmed operation user, success says `Created <path>` and re-reads
  // the directory rather than patching it, and a failure stays on screen as an
  // Alert. `create` is O_EXCL: it never replaces the entry it collided with,
  // unlike an upload's write.

  const newEntryError =
    newEntry === undefined ? undefined : newEntryNameError(newEntry.kind, newEntry.name, currentListing?.entries ?? []);

  const createEntry = useCallback(async () => {
    const pending = newEntry;
    if (pending === undefined || pending.name === '' || newEntryError !== undefined) {
      return;
    }
    const target = joinPath(path, pending.name);
    setWriteError(undefined);
    setCreating(true);
    try {
      if (pending.kind === 'directory') {
        await createVolumeDirectory(composeId, source, target, access);
      } else {
        await createVolumeFile(composeId, source, target, access);
      }
      setNewEntry(undefined);
      message.success(`Created ${target}`);
      await loadList();
    } catch (reason: unknown) {
      setNewEntry(undefined);
      setWriteError(newEntryFailure(pending.kind, reason));
    } finally {
      setCreating(false);
    }
  }, [access, composeId, createVolumeDirectory, createVolumeFile, loadList, message, newEntry, newEntryError, path, source]);

  // ---- The editor ------------------------------------------------------------
  //
  // Opening is the row action's meaning, never the row click's; the 1 MB gate
  // runs before the fetch, and the fetched bytes are decoded by the view
  // helper's binary rule. Any read failure keeps the screen's established
  // presentation (the API's own words in the open alert).

  const editEntry = useCallback(
    async (entry: VolumeEntry) => {
      const opening = { request: ++editorRead.current.request, context: editorContext };
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
        const bytes = await readVolumeFile(composeId, source, target, access);
        if (!editorReadCurrent(opening, editorRead.current)) {
          return;
        }
        const content = editorContent(target, bytes);
        if (content.status === 'refused') {
          setEditorRefusal({ failure: content.failure, path: target });
          return;
        }
        setEditing({ session: ++editorSession.current, draft: content.text, original: content.text, path: target });
      } catch (reason: unknown) {
        if (!editorReadCurrent(opening, editorRead.current)) {
          return;
        }
        setOpenError({ entry, error: failureMessage(reason) });
      }
    },
    [access, composeId, editorContext, path, readVolumeFile, source],
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
        access,
      );
      const size = new TextEncoder().encode(editing.draft).length;
      const name = editing.path.slice(editing.path.lastIndexOf('/') + 1);
      setEditing((current) => savedEditorState(current, editing.session, editing.draft));
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
      if (
        editorRead.current.context === editorContext &&
        editorSessionCurrent(currentEditor.current, editing.session)
      ) {
        setSaveError(editFailure(reason));
      }
    }
  }, [access, composeId, editing, editorContext, listingKey, message, source, writeVolumeFile]);

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
  // New folder, New file, upload and restore are the toolbar's write controls;
  // editing and deletion belong to the row whose file they touch. The download
  // actions stay anchors or a re-read, and a missing source disables
  // everything with the API's own reason rather than letting a mount recreate
  // it. A write is a PUT or a DELETE, never a GET, so the session cookie keeps
  // it CSRF-safe.

  const toolbar = (
    <Flex gap={8} wrap>
      <Tooltip
        title={
          refusal ?? 'Creates a folder in this directory, owned by the operation user you confirmed.'
        }
      >
        <Button
          disabled={refusal !== undefined}
          icon={<FolderAddOutlined />}
          onClick={() => setNewEntry({ kind: 'directory', name: '' })}
        >
          New folder
        </Button>
      </Tooltip>
      <Tooltip
        title={
          refusal ??
          'Creates an empty file in this directory, owned by the operation user you confirmed. An existing name is refused, never replaced.'
        }
      >
        <Button
          disabled={refusal !== undefined}
          icon={<FileAddOutlined />}
          onClick={() => setNewEntry({ kind: 'file', name: '' })}
        >
          New file
        </Button>
      </Tooltip>
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
              ? volumeReadUrl(composePath, source, joinPath(path, selectedRow.entry.name), access)
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
          href={refusal === undefined ? volumeArchiveUrl(composePath, source, path, access) : undefined}
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
        <Flex align="center" gap={8} wrap>
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
              <Flex align="center" gap={8} key={`${use.service}:${use.target}`}>
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
          <Flex align="center" gap={8} justify="flex-end" onClick={(event) => event.stopPropagation()}>
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
        title="Could not resolve this Compose’s Volumes"
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
        <Alert description={view.message} title="This Volume’s source is missing" showIcon type="error" />
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
            title={runningWarning.message}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="warning"
          />
        )}
        {failure && (
          <Alert
            closable={{ afterClose: () => setOpenError(undefined) }}
            description={failure.description}
            title={failure.message}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type={openError && isSymlink(openError.entry) && isEscapeRefusal(openError.error) ? 'warning' : 'error'}
          />
        )}
        {editorRefusal && (
          <Alert
            action={
              <Button href={volumeReadUrl(composePath, source, editorRefusal.path)}>
                Download
              </Button>
            }
            closable={{ afterClose: () => setEditorRefusal(undefined) }}
            description={editorRefusal.failure.description}
            title={editorRefusal.failure.message}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            type="info"
          />
        )}
        {writeError && (
          <Alert
            closable={{ afterClose: () => setWriteError(undefined) }}
            description={writeError.description}
            title={writeError.message}
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
          extra={confirmed ? toolbar : undefined}
          title={<Crumbs onGo={goTo} path={path} root={volumeLabel(view.volume)} />}
          variant="outlined"
        >
          {!confirmed ? (
            <EmptyState
              description="Pick the Service account these operations should run as — or a numeric uid:gid — acknowledge anything it requires, then press Confirm to load this directory."
              title="Choose an operation user"
            />
          ) : currentListing?.error !== undefined ? (
            <Alert
              action={<Button onClick={() => void loadList()}>Retry</Button>}
              description={currentListing.error}
              title="Could not read this directory"
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
          <Paragraph style={{ marginBottom: 0, marginTop: LAYOUT.gutterSmall }} type="secondary">
            New folders and files are created here as the confirmed operation user, uploads land in this directory,
            and deletes are permanent — there is no trash. A file opens in the editor through its own Edit action;
            files past the editor’s 1 MB cap and binary files are refused there rather than opened. Paths come from
            this Compose’s own resolution, so the panel never accepts a host path from a form, and downloads stream
            straight to disk.
          </Paragraph>
        </Card>
      </>
    );
  }

  return (
    <>
      {header}
      {identityVolume && (
        <Card
          extra={
            refusal === undefined ? (
              <Button disabled={!formReady || appliedIsCurrent} onClick={confirmOperationUser} type="primary">
                Confirm
              </Button>
            ) : undefined
          }
          style={{ marginBottom: LAYOUT.gutterSmall }}
          title="Operation user"
        >
          <Flex gap={LAYOUT.gutterSmall} vertical>
            <Select
              onChange={(value) => {
                setIdentity(value);
                setRootAck('');
                setBindAck('');
              }}
              options={[
                ...mountingUsers.map((user) => ({
                  value: user.service,
                  label: `${user.service} — ${user.user}`,
                })),
                { value: '__override', label: 'Numeric uid:gid override' },
              ]}
              placeholder="Choose a mounting Service or numeric uid:gid"
              value={identity || undefined}
            />
            {identity === '__override' && (
              <Input
                onChange={(event) => {
                  setOverride(event.target.value);
                  setRootAck('');
                  setBindAck('');
                }}
                placeholder="1000:1000"
                value={override}
              />
            )}
            {chosenRefusal && <Alert showIcon title={chosenRefusal} type="warning" />}
            {chosen?.warning && <Alert showIcon title={chosen.warning} type="warning" />}
            {rootRequired && (
              <Checkbox
                checked={rootAck === identityKey}
                onChange={(event) => setRootAck(event.target.checked ? identityKey : '')}
              >
                Use root (UID 0). Files created by these operations will be root-owned.
              </Checkbox>
            )}
            {confirmationRequired && (
              <Checkbox
                checked={bindAck === riskKey}
                onChange={(event) => setBindAck(event.target.checked ? riskKey : '')}
              >
                Mount this unverifiable bind path. If missing, Docker creates it as root:root 0755.{' '}
                {identityVolume.preflight.warning}
              </Checkbox>
            )}
            {applied !== undefined && !appliedIsCurrent && (
              <Text type="secondary">
                The directory and its operations keep running as {confirmedLabel} until you press Confirm.
              </Text>
            )}
          </Flex>
        </Card>
      )}
      {body}

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
            <ul style={{ marginBottom: 0, paddingInlineStart: LAYOUT.gutter }}>
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

      <Modal
        cancelText="Cancel"
        confirmLoading={creating}
        okButtonProps={{ disabled: (newEntry?.name ?? '') === '' || newEntryError !== undefined }}
        okText="Create"
        onCancel={() => setNewEntry(undefined)}
        onOk={() => void createEntry()}
        open={newEntry !== undefined}
        title={newEntry?.kind === 'directory' ? 'New folder' : 'New file'}
      >
        {newEntry && (
          <Flex gap={8} vertical>
            <Text type="secondary">
              Creates a {newEntry.kind === 'directory' ? 'folder' : 'file'} in{' '}
              <Text code>{restoreTarget(path)}</Text>. The entry is owned by{' '}
              <Text code>{confirmedUser ?? 'the confirmed operation user'}</Text> — the operation user you
              confirmed.
            </Text>
            <Input
              autoFocus
              maxLength={255}
              onChange={(event) => setNewEntry((current) => current && { ...current, name: event.target.value })}
              onPressEnter={() => void createEntry()}
              placeholder={newEntry.kind === 'directory' ? 'Folder name' : 'File name'}
              status={newEntryError ? 'error' : undefined}
              value={newEntry.name}
            />
            {newEntryError && <Text type="danger">{newEntryError}</Text>}
          </Flex>
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
