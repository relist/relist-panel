import { Typography } from 'antd';
import type { ZeroDowntimeService } from '../data/types';
import { ZeroDowntimeRequirements } from './ZeroDowntimeRequirements';

const { Paragraph, Text } = Typography;

// Static counterpart to the YAML risk warnings: what to avoid in a Compose file.
// Low emphasis on purpose, so it does not compete with the editor; paths wrap at narrow widths.
// With a zero-downtime switch on, it also lists what rollout needs and where each switched-on
// Service stands (ADR-0023); the Domains tab and the refusal modal carry the louder warnings.
export function ComposeEditorGuide({ zeroDowntime }: { zeroDowntime?: ZeroDowntimeService[] } = {}) {
  const enabled = zeroDowntime?.filter((s) => s.enabled) ?? [];
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
      {enabled.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <Text strong type="secondary">
            Zero-downtime deploy
          </Text>
          <ZeroDowntimeRequirements />
          {enabled.map((s) =>
            s.reasons.length === 0 ? (
              <div key={s.service}>
                <Text type="secondary">{`${s.service} — ready`}</Text>
              </div>
            ) : (
              <div key={s.service}>
                <Text type="warning">{`${s.service} — ${s.reasons.join('; ')}`}</Text>
              </div>
            ),
          )}
        </div>
      )}
    </Paragraph>
  );
}
