import { Button, Flex, Modal, Typography } from 'antd';

const { Paragraph, Text } = Typography;

export type BuildSnippet = 'args' | 'secrets';

export const ARGS_SNIPPET = `services:
  api:
    build:
      context: ./src/api
      args:
        API_URL: \${API_URL}   # from Environment or Compose Variables`;

export const SECRETS_SNIPPET = `services:
  api:
    build:
      context: ./src/api
      secrets: [npm_token]     # Dockerfile: RUN --mount=type=secret,id=npm_token ...
secrets:
  npm_token:
    environment: NPM_TOKEN     # a Compose Variable, flagged secret`;

/**
 * The text appended to a draft for a snippet. Merging into a Service the draft
 * already has is the operator's call, so it goes in as comments: the YAML stays
 * valid and nothing the operator wrote is touched.
 */
export function snippetComment(snippet: BuildSnippet): string {
  const body = snippet === 'args' ? ARGS_SNIPPET : SECRETS_SNIPPET;
  const title = snippet === 'args' ? 'Build args' : 'Build secret';
  return [`# ${title} example, merge into your Service and uncomment:`, ...body.split('\n').map((l) => `# ${l}`)].join('\n');
}

function Snippet({ onInsert, text, title }: { onInsert: () => void; text: string; title: string }) {
  return (
    <Flex gap={6} vertical>
      <Flex align="center" justify="space-between">
        <Text strong>{title}</Text>
        <Button onClick={onInsert} size="small">
          Insert {title.toLowerCase()} into editor
        </Button>
      </Flex>
      <Paragraph copyable={{ text }} style={{ marginBottom: 0 }}>
        <pre style={{ margin: 0, fontSize: 12, whiteSpace: 'pre-wrap' }}>{text}</pre>
      </Paragraph>
    </Flex>
  );
}

/** Help for build args and secrets, with both snippets insertable into the editor draft. */
export function BuildSnippetsModal({
  onClose,
  onInsert,
  open,
}: {
  onClose: () => void;
  onInsert: (snippet: BuildSnippet) => void;
  open: boolean;
}) {
  return (
    <Modal footer={<Button onClick={onClose}>Close</Button>} onCancel={onClose} open={open} title="Build args & secrets" width={640}>
      <Flex gap={16} vertical>
        <Paragraph style={{ marginBottom: 0 }} type="secondary">
          <Text code>build.args</Text> interpolates <Text code>{'${VAR}'}</Text> from Environment and Compose Variables. A
          build arg&apos;s value is visible in the image&apos;s history; use a build secret for anything private.
        </Paragraph>
        <Snippet onInsert={() => onInsert('args')} text={ARGS_SNIPPET} title="Args" />
        <Snippet onInsert={() => onInsert('secrets')} text={SECRETS_SNIPPET} title="Secrets" />
      </Flex>
    </Modal>
  );
}
