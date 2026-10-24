import { Typography } from 'antd';

const { Paragraph, Text } = Typography;

// Static counterpart to the YAML risk warnings: what to avoid in a Compose file.
// Low emphasis on purpose, so it does not compete with the editor; paths wrap at narrow widths.
export function ComposeEditorGuide() {
  return (
    <Paragraph type="secondary" style={{ fontSize: 12, margin: 0, overflowWrap: 'anywhere', padding: '8px 12px' }}>
      <ul style={{ margin: 0, paddingInlineStart: 16 }}>
        <li>
          Containers should not write into <Text code>./src</Text> — use a named volume.
        </li>
        <li>
          To share a volume between Composes, declare it <Text code>external: true</Text>.
        </li>
        <li>
          Relative bind mounts such as <Text code>./data</Text> live in this Compose's directory and are deleted with
          the Compose.
        </li>
      </ul>
    </Paragraph>
  );
}
