import { CloseOutlined, FolderOpenOutlined } from '@ant-design/icons';
import { Alert, App, Button, Flex, Tag, Tooltip, Typography } from 'antd';
import { useCallback } from 'react';
import { navigate, paths } from '../app/router';
import { useNow } from '../app/useNow';
import { IDLE_TIMEOUT_MINUTES, usePanel } from '../data/store';
import { findComposeLocation, idleMinutesLeft } from '../data/types';

const { Text } = Typography;

/**
 * Volume Handles that are still open.
 *
 * A handle keeps a container alive with a Volume mounted, and it does not close
 * when the operator leaves the page that opened it. That is the design — closing
 * the browser tab must not yank the mount out from under a download in progress —
 * but it means the only honest way to keep them from accumulating is to say, on
 * the screen the operator opens first, which ones are still there.
 *
 * The idle countdown is part of the message rather than a detail: a handle that
 * is about to stop itself needs no action, and one with 28 minutes left does.
 */
export function LiveHandlesBanner() {
  const { handles, isPending, projects, stopHandle } = usePanel();
  const { message } = App.useApp();
  const now = useNow();

  const open = Object.values(handles).map((handle) => {
    const location = findComposeLocation(projects, handle.composeId);
    return { handle, location };
  });

  const stop = useCallback(
    (volumeId: string, name: string) => {
      void stopHandle(volumeId).then(() => message.success(`Closed ${name}`));
    },
    [message, stopHandle],
  );

  if (open.length === 0) {
    return null;
  }

  return (
    <Alert
      description={
        <Flex vertical gap={8} style={{ marginTop: 8 }}>
          {open.map(({ handle, location }) => {
            const remaining = idleMinutesLeft(handle, now, IDLE_TIMEOUT_MINUTES);
            const name = location?.compose.name ?? 'a deleted Compose';
            const volume = handle.volumeId.split(':').slice(1).join(':');
            return (
              <Flex align="center" gap={8} justify="space-between" key={handle.volumeId} wrap>
                <Flex align="center" gap={8} wrap>
                  {location ? (
                    <Button
                      icon={<FolderOpenOutlined />}
                      onClick={() =>
                        navigate(
                          paths.volumeExplorer(
                            location.project.id,
                            location.environment.id,
                            location.compose.id,
                            handle.volumeId,
                          ),
                        )
                      }
                      size="small"
                      type="link"
                    >
                      {name} / {volume}
                    </Button>
                  ) : (
                    <Text>
                      {name} / {volume}
                    </Text>
                  )}
                  <Tag color={handle.mode === 'rw' ? 'processing' : 'default'}>
                    {handle.mode === 'rw' ? 'read and write' : 'read only'}
                  </Tag>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    uid {handle.uid}:{handle.gid}
                  </Text>
                  {remaining !== undefined && (
                    <Tooltip title={`Stops itself after ${IDLE_TIMEOUT_MINUTES} minutes without a request. A read or a write restarts the clock.`}>
                      <Text type={remaining <= 5 ? 'danger' : 'secondary'} style={{ fontSize: 12 }}>
                        closes in {remaining} min
                      </Text>
                    </Tooltip>
                  )}
                </Flex>
                <Button
                  icon={<CloseOutlined />}
                  loading={isPending(`handle-stop:${handle.volumeId}`)}
                  onClick={() => stop(handle.volumeId, volume)}
                  size="small"
                >
                  Close
                </Button>
              </Flex>
            );
          })}
        </Flex>
      }
      message={`${open.length} Volume ${open.length === 1 ? 'Handle is' : 'Handles are'} still open`}
      showIcon
      style={{ marginBottom: 24 }}
      type="info"
    />
  );
}
