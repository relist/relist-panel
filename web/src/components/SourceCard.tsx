import { Button, Card, Flex, Tag, Typography } from 'antd';

import { buildServices, sourceCardState } from '../data/source-view';
import type { ComposeSource, Deployment } from '../data/types';
import { RelativeTime } from './primitives';

const { Text } = Typography;

function formatSize(bytes: number): string {
  if (bytes >= 1 << 20) {
    return `${(bytes / (1 << 20)).toFixed(1)} MiB`;
  }
  return `${Math.max(1, Math.round(bytes / 1024))} KiB`;
}

/**
 * The Editor tab's Source card: what the Compose builds from, whether it has
 * been built, and the Services the YAML builds. Opening the modals and the
 * Deployments tab is the page's job.
 */
export function SourceCard({
  draft,
  lastBuilt,
  onConfigure,
  onShowSnippets,
  onUploadNew,
  onViewDeployments,
  source,
}: {
  draft: string;
  lastBuilt: Deployment | undefined;
  onConfigure: () => void;
  onShowSnippets: () => void;
  onUploadNew: () => void;
  onViewDeployments: () => void;
  source: ComposeSource | null;
}) {
  const state = sourceCardState(source, lastBuilt);

  if (!source) {
    return (
      <Card title="Source" variant="outlined">
        <Flex align="center" gap={8} justify="space-between" wrap>
          <Text type="secondary">Services use images named in the YAML</Text>
          <Button onClick={onConfigure} size="small" type="primary">
            + Add source
          </Button>
        </Flex>
      </Card>
    );
  }

  const targets = buildServices(draft);
  const built = lastBuilt?.status === 'succeeded' ? lastBuilt : undefined;

  return (
    <Card
      extra={
        <Button onClick={onConfigure} size="small" type="link">
          Configure
        </Button>
      }
      title="Source"
      variant="outlined"
    >
      <Flex gap={8} vertical>
        <Text strong style={{ wordBreak: 'break-all' }}>
          {state.title}
        </Text>

        {source.kind === 'git' ? (
          <>
            {source.lastTest && (
              <Text style={{ fontSize: 12 }} type={source.lastTest.ok ? 'success' : 'danger'}>
                {source.lastTest.ok ? 'Connected' : 'Connection failed'} · <RelativeTime value={source.lastTest.at} />
              </Text>
            )}
            <Flex align="center" gap={6} wrap>
              <Text style={{ fontSize: 12 }} type="secondary">
                {state.builtLine}
                {built?.finishedAt && (
                  <>
                    {' · '}
                    <RelativeTime value={built.finishedAt} />
                  </>
                )}
              </Text>
              {state.newCommits && <Tag color="blue">New commits</Tag>}
            </Flex>
          </>
        ) : (
          <>
            {source.zip && (
              <Text style={{ fontSize: 12 }} type="secondary">
                {formatSize(source.zip.size)} · {source.zip.files} files · uploaded <RelativeTime value={source.zip.uploadedAt} />
              </Text>
            )}
            <Text style={{ fontSize: 12 }} type="secondary">
              {state.notBuilt ? 'Not built yet' : 'Built from this upload'}
            </Text>
          </>
        )}

        <Flex gap={4} wrap>
          {targets.length > 0 ? (
            targets.map((name) => <Tag key={name}>{name}</Tag>)
          ) : (
            <Text style={{ fontSize: 12 }} type="secondary">
              No Service has build: yet.{' '}
              <Button onClick={onShowSnippets} size="small" style={{ padding: 0 }} type="link">
                Build args &amp; secrets
              </Button>
            </Text>
          )}
        </Flex>

        <Flex gap={4} wrap>
          {source.kind === 'zip' && (
            <Button onClick={onUploadNew} size="small">
              Upload new zip
            </Button>
          )}
          <Button onClick={onViewDeployments} size="small" type="link">
            View deployments →
          </Button>
        </Flex>
      </Flex>
    </Card>
  );
}
