import { InboxOutlined, LoadingOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Input, Progress, Table, Tag, Typography, Upload, theme } from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { CODE_FONT_STACK } from '../app/theme';
import { formatBytes } from '../components/MetricChart';
import { EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { IMAGES_URL, listImages, uploadImage } from '../data/api';
import type { HostImage } from '../data/types';
import { checkImageArchive, filterImages, shortImageId } from './images-view';

const { Text } = Typography;

type UploadState =
  | { kind: 'idle' }
  | { kind: 'uploading'; name: string; sent: number; total: number }
  | { kind: 'loading'; name: string }
  | { kind: 'loaded'; names: string[] }
  | { kind: 'failed'; message: string };

/**
 * Images (GUIDELINES §4.11): the host's Docker images and the one way to add
 * to them, loading a `docker save` archive. The list is the page's first SWR
 * user; the upload is a small state machine that mirrors the spec's five
 * states, and a successful load refetches the list and highlights its rows.
 */
export function ImagesPage() {
  const { token } = theme.useToken();
  const { data, error, isLoading, mutate } = useSWR(IMAGES_URL, listImages);
  const [upload, setUpload] = useState<UploadState>({ kind: 'idle' });
  const [query, setQuery] = useState('');
  const abort = useRef<AbortController | undefined>(undefined);

  // The unmount cleanup and the upload callbacks read these refs, not the
  // render's closure, so they see the current state.
  const mounted = useRef(true);
  const uploadKind = useRef<UploadState['kind']>('idle');
  uploadKind.current = upload.kind;

  // In-app navigation does not fire `beforeunload`, so leaving the page must
  // stop an upload still sending bytes. Once everything is sent (`loading`) the
  // abort would make the daemon discard a complete archive, so it is left alone.
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (uploadKind.current === 'uploading') abort.current?.abort();
    };
  }, []);

  const busy = upload.kind === 'uploading' || upload.kind === 'loading';

  // Leaving the page mid-upload would drop the archive, so the browser asks
  // first — only while bytes are in flight or the daemon is loading them.
  useEffect(() => {
    if (!busy) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [busy]);

  const start = async (file: File) => {
    const problem = checkImageArchive(file);
    if (problem) {
      setUpload({ kind: 'failed', message: problem });
      return;
    }
    const controller = new AbortController();
    abort.current = controller;
    setUpload({ kind: 'uploading', name: file.name, sent: 0, total: file.size });
    try {
      const names = await uploadImage(
        file,
        (sent, total) =>
          setUpload((current) => {
            if (current.kind !== 'uploading') return current;
            return sent >= total ? { kind: 'loading', name: current.name } : { ...current, sent, total };
          }),
        controller.signal,
      );
      void mutate();
      if (mounted.current) setUpload({ kind: 'loaded', names });
    } catch (reason: unknown) {
      if (mounted.current) setUpload({ kind: 'failed', message: reason instanceof Error ? reason.message : 'Upload failed' });
    } finally {
      abort.current = undefined;
    }
  };

  const loadedNames = upload.kind === 'loaded' ? upload.names : [];
  const rows = useMemo(() => filterImages(data ?? [], query), [data, query]);

  const columns: TableColumnsType<HostImage> = [
    {
      key: 'image',
      render: (_, image) =>
        image.tags.length === 0 ? (
          <Text type="secondary">&lt;none&gt;</Text>
        ) : (
          <Flex gap={4} vertical>
            {image.tags.map((tag) => (
              <Text copyable key={tag}>
                {tag}
              </Text>
            ))}
          </Flex>
        ),
      title: 'Image',
    },
    {
      dataIndex: 'id',
      key: 'id',
      render: (id: string) => <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{shortImageId(id)}</Text>,
      title: 'ID',
    },
    { dataIndex: 'size', key: 'size', render: (size: number) => formatBytes(size), title: 'Size' },
    { dataIndex: 'created', key: 'created', render: (created: string) => <RelativeTime value={created} />, title: 'Created' },
    {
      dataIndex: 'inUse',
      key: 'inUse',
      render: (inUse: boolean) => (inUse ? <Tag color="success">In use</Tag> : null),
      title: 'In use',
    },
  ];

  return (
    <>
      <style>{`.image-row-loaded > td { background: ${token.colorSuccessBg} !important; }`}</style>
      <PageHeader
        description={
          <>
            Load an image built with <Text code>docker save</Text> onto this host, then reference it by name in a Compose
            file.
          </>
        }
        extra={
          <Button icon={<ReloadOutlined />} loading={isLoading} onClick={() => void mutate()}>
            Refresh
          </Button>
        }
        title="Images"
      />

      <Card style={{ marginBottom: 16 }} variant="outlined">
        <Flex gap={12} vertical>
          {upload.kind === 'uploading' && (
            <Flex align="center" gap={16} wrap>
              <Text strong>{upload.name}</Text>
              <Progress
                percent={Math.floor((upload.sent / Math.max(upload.total, 1)) * 100)}
                style={{ flex: 1, minWidth: 200 }}
              />
              <Button onClick={() => abort.current?.abort()}>Cancel</Button>
            </Flex>
          )}
          {upload.kind === 'loading' && (
            <Flex align="center" gap={12}>
              <LoadingOutlined />
              <Text>Loading into Docker…</Text>
              <Text type="secondary">{upload.name}</Text>
            </Flex>
          )}
          {upload.kind === 'loaded' && (
            <Alert
              description={
                <Flex gap={4} vertical>
                  {upload.names.map((name) => (
                    <Text copyable key={name}>
                      {name}
                    </Text>
                  ))}
                </Flex>
              }
              showIcon
              title={upload.names.length === 1 ? 'Loaded 1 image' : `Loaded ${upload.names.length} images`}
              type="success"
            />
          )}
          {upload.kind === 'failed' && <Alert showIcon title={upload.message} type="error" />}
          <Upload.Dragger
            beforeUpload={(file) => {
              void start(file);
              return false;
            }}
            disabled={busy}
            maxCount={1}
            showUploadList={false}
          >
            <p className="ant-upload-drag-icon">
              <InboxOutlined />
            </p>
            <p className="ant-upload-text">Click or drag an image archive here</p>
            <p className="ant-upload-hint">.tar, .tar.gz, .tgz, .tar.zst, .tar.xz or .tar.bz2, up to 10 GiB</p>
          </Upload.Dragger>
        </Flex>
      </Card>

      {error && (
        <Alert
          action={<Button onClick={() => void mutate()}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={error instanceof Error ? error.message : 'Could not load images'}
          type="error"
        />
      )}

      <Card variant="outlined">
        <Input
          allowClear
          prefix={<SearchOutlined />}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Filter by tag"
          style={{ marginBottom: 16, maxWidth: 320 }}
          value={query}
        />
        <Table<HostImage>
          columns={columns}
          dataSource={rows}
          loading={isLoading}
          locale={{
            emptyText: isLoading || error ? (
              ' '
            ) : (
              <EmptyState
                description={
                  query.trim() === ''
                    ? 'Load an archive above to add the first image to this host.'
                    : 'No image has a tag matching that filter.'
                }
                title={query.trim() === '' ? 'No images on this host' : 'No matching images'}
              />
            ),
          }}
          pagination={false}
          rowClassName={(image) =>
            loadedNames.some((name) => name === image.id || image.tags.includes(name)) ? 'image-row-loaded' : ''
          }
          rowKey="id"
        />
      </Card>
    </>
  );
}
