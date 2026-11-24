import { Alert, Button, Card, Flex, Modal, Progress, Space, Typography } from 'antd';

import { LAYOUT } from '../app/theme';
import type { VolumeUpload } from '../data/types';
import type { ActiveUpload } from './useVolumeUpload';
import { uploadEta, uploadProgressText, uploadRate, volumeUploadFailure } from './volume-explorer-view';

type Controls = {
  active?: ActiveUpload;
  resumePrompt?: { file: File; target: string; match: VolumeUpload };
  paused?: { file: File; target: string; id: string; offset: number };
  failed?: { target: string; upload: VolumeUpload };
  openUploads: VolumeUpload[];
  resume: () => void;
  startOver: () => void;
  dismissPrompt: () => void;
  cancel: () => void;
  resumePaused: () => void;
  retry: () => void;
  discard: (id: string) => void;
};

function percent(sent: number, total: number): number {
  return total === 0 ? 100 : Math.floor((sent / total) * 100);
}

function age(updatedAt: string): string {
  const minutes = Math.max(0, Math.round((Date.now() - Date.parse(updatedAt)) / 60_000));
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  return `${hours} h ago`;
}

/**
 * Everything the Volume Explorer shows about chunked uploads (ADR-0053): the
 * resume question for a picked file that matches an unfinished upload, the
 * card of the upload in progress, a paused upload, a write that failed after
 * every byte arrived, and the list of this User's unfinished uploads.
 */
export function VolumeUploadStatus(props: Controls) {
  const { active, resumePrompt, paused, failed, openUploads } = props;
  const now = Date.now();
  const unfinished = openUploads.filter(
    (u) => u.state !== 'consuming' && u.id !== paused?.id && u.id !== failed?.upload.id && u.id !== resumePrompt?.match.id,
  );

  return (
    <>
      <Modal
        footer={[
          <Button key="cancel" onClick={props.dismissPrompt}>
            Cancel
          </Button>,
          <Button key="over" onClick={props.startOver}>
            Start over
          </Button>,
          <Button key="resume" onClick={props.resume} type="primary">
            Resume
          </Button>,
        ]}
        onCancel={props.dismissPrompt}
        open={resumePrompt !== undefined}
        title="Resume this upload?"
      >
        {resumePrompt && (
          <Typography.Paragraph>
            <Typography.Text code>{resumePrompt.file.name}</Typography.Text> was uploaded to{' '}
            {percent(resumePrompt.match.offset, resumePrompt.match.size)}% into{' '}
            <Typography.Text code>{resumePrompt.target}</Typography.Text>. Resume sends only the rest; Start over
            discards what the panel holds and sends the whole file.
          </Typography.Paragraph>
        )}
      </Modal>

      {active && (
        <Card size="small" style={{ marginBottom: LAYOUT.gutterSmall }} title={`Uploading ${active.name}`}>
          {active.phase === 'writing' ? (
            <Flex vertical gap={4}>
              <Progress percent={100} showInfo={false} status="active" />
              <Typography.Text type="secondary">Writing into the Volume…</Typography.Text>
            </Flex>
          ) : (
            <Flex vertical gap={4}>
              <Progress percent={percent(active.sent, active.total)} status="active" />
              <Flex justify="space-between" wrap gap={8}>
                <Typography.Text type="secondary">
                  {active.phase === 'offline'
                    ? 'Waiting for the network…'
                    : [
                        uploadProgressText(active.sent, active.total),
                        uploadRate(active.sent - active.startSent, now - active.startedAt),
                        uploadEta(active.sent - active.startSent, now - active.startedAt, active.total - active.startSent),
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                </Typography.Text>
                <Button danger onClick={props.cancel} size="small">
                  Cancel
                </Button>
              </Flex>
            </Flex>
          )}
        </Card>
      )}

      {paused && !active && (
        <Alert
          action={
            <Space>
              <Button onClick={props.resumePaused} size="small" type="primary">
                Resume
              </Button>
              <Button onClick={() => props.discard(paused.id)} size="small">
                Discard
              </Button>
            </Space>
          }
          description={`${uploadProgressText(paused.offset, paused.file.size)} of ${paused.file.name} is stored on the panel. Resume when the connection is back.`}
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          title="Upload paused"
          type="warning"
        />
      )}

      {failed && !active && (
        <Alert
          action={
            <Space>
              <Button onClick={props.retry} size="small" type="primary">
                Retry
              </Button>
              <Button onClick={() => props.discard(failed.upload.id)} size="small">
                Discard
              </Button>
            </Space>
          }
          description={volumeUploadFailure(failed.upload).description}
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          title={volumeUploadFailure(failed.upload).message}
          type="error"
        />
      )}

      {unfinished.length > 0 && (
        <Alert
          description={
            <Flex vertical gap={4}>
              <Typography.Text type="secondary">
                To resume, upload the same file into the same directory again. A browser cannot reopen a file by
                itself after a reload.
              </Typography.Text>
              {unfinished.map((u) => (
                <Flex align="center" gap={8} justify="space-between" key={u.id} wrap>
                  <Typography.Text>
                    <Typography.Text code>{u.path}</Typography.Text> — {percent(u.offset, u.size)}%
                    {u.state === 'failed' ? ', write failed' : ''}, {age(u.updatedAt)}
                  </Typography.Text>
                  <Button onClick={() => props.discard(u.id)} size="small">
                    Discard
                  </Button>
                </Flex>
              ))}
            </Flex>
          }
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          title="Unfinished uploads"
          type="info"
        />
      )}
    </>
  );
}
