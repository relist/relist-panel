import { Alert, Skeleton, Typography } from 'antd';

import type { RemovalPlanEntry } from '../data/types';

const { Paragraph, Text } = Typography;

/**
 * What a Delete takes: for each Compose its removed volumes, the shared volumes
 * it keeps and why, and its directory (which holds relative bind mounts such as
 * `./data`). Shown in a DangerModal's `consequences`; the delete itself still
 * uses the server's own plan when it runs.
 */
export function RemovalPlanList({
  entries,
  error,
  fallbackDirectories,
  loading,
}: {
  entries?: RemovalPlanEntry[];
  error?: string;
  /** The directories the page already knows; listed when the plan could not be loaded. */
  fallbackDirectories?: string[];
  loading?: boolean;
}) {
  if (loading) {
    return <Skeleton active paragraph={{ rows: 3 }} title={{ width: 160 }} />;
  }
  if (error) {
    return (
      <>
        <Alert
          description={`${error} The delete still removes what its own plan says when you confirm.`}
          showIcon
          style={{ marginBottom: 16 }}
          title="Could not load what this delete removes."
          type="warning"
        />
        {fallbackDirectories && fallbackDirectories.length > 0 && (
          <>
            <Paragraph style={{ marginBottom: 4 }} strong>
              Directory removed
            </Paragraph>
            <ul style={{ marginTop: 0 }}>
              {fallbackDirectories.map((directory, index) => (
                <li key={`${index}:${directory}`}>
                  <Text code>{directory}</Text>
                </li>
              ))}
            </ul>
          </>
        )}
      </>
    );
  }
  if (!entries || entries.length === 0) {
    return <Paragraph>There are no Composes to tear down.</Paragraph>;
  }
  const multiple = entries.length > 1;
  return (
    <div>
      {entries.map((entry) => (
        <div key={entry.composeId} style={{ marginBottom: 12 }}>
          {multiple && <Text strong>{entry.name}</Text>}
          {entry.error && (
            <Paragraph style={{ marginBottom: 4 }} type="warning">
              {entry.error}
            </Paragraph>
          )}
          {!entry.error && entry.removeVolumes.length === 0 && entry.keepVolumes.length === 0 && (
            <Paragraph style={{ marginBottom: 4 }} type="secondary">
              No named volumes.
            </Paragraph>
          )}
          {entry.removeVolumes.length > 0 && (
            <>
              <Paragraph style={{ marginBottom: 4 }} strong>
                Volumes removed
              </Paragraph>
              <ul style={{ marginTop: 0, marginBottom: 8 }}>
                {entry.removeVolumes.map((volume) => (
                  <li key={volume}>
                    <Text code>{volume}</Text>
                  </li>
                ))}
              </ul>
            </>
          )}
          {entry.keepVolumes.length > 0 && (
            <>
              <Paragraph style={{ marginBottom: 4 }} strong>
                Volumes kept (used elsewhere)
              </Paragraph>
              <ul style={{ marginTop: 0, marginBottom: 8 }}>
                {entry.keepVolumes.map((volume) => (
                  <li key={volume.name}>
                    <Text code>{volume.name}</Text> <Text type="secondary">{volume.reason}</Text>
                  </li>
                ))}
              </ul>
            </>
          )}
          <Paragraph style={{ marginBottom: 4 }} strong>
            Directory removed
          </Paragraph>
          <Paragraph style={{ marginBottom: 0 }}>
            <Text code>{entry.directory}</Text>
          </Paragraph>
        </div>
      ))}
      <Paragraph style={{ marginBottom: 0 }} type="secondary">
        The directory holds everything inside it, including relative bind mounts such as <Text code>./data</Text>.
      </Paragraph>
    </div>
  );
}
