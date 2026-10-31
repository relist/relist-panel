import { Alert, Button, Card, Flex, Modal, Switch, Typography } from 'antd';
import { useState } from 'react';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { usePanel } from '../data/store';
import type { Compose, ZeroDowntimeService } from '../data/types';
import { ZeroDowntimeRequirements } from './ZeroDowntimeRequirements';

const { Paragraph, Text } = Typography;

function errorText(reason: unknown, fallback: string) {
  return reason instanceof Error ? reason.message : fallback;
}

function Reasons({ reasons }: { reasons: string[] }) {
  return (
    <ul style={{ margin: 0, paddingInlineStart: 20 }}>
      {reasons.map((reason) => (
        <li key={reason}>{reason}</li>
      ))}
    </ul>
  );
}

/**
 * The Domains tab's zero-downtime switches, one per Service with a Domain
 * (ADR-0023). Turning one on asks first; turning one off saves at once. The
 * page owns the list, so one read serves the card, the editor note and the
 * Fresh volumes modal; the card asks the page to read again after a change.
 */
export function ZeroDowntimeCard({
  compose,
  error: loadError,
  onOpenEditor,
  onReload,
  services,
}: {
  compose: Compose;
  /** Why the last read failed. */
  error?: string;
  onOpenEditor: () => void;
  onReload: () => Promise<void>;
  /** Undefined until the first read ends. */
  services?: ZeroDowntimeService[];
}) {
  const { setZeroDowntime } = usePanel();
  const [saving, setSaving] = useState<string>();
  const [saveError, setSaveError] = useState<string>();
  const [confirm, setConfirm] = useState<ZeroDowntimeService>();
  const [confirmError, setConfirmError] = useState<string>();
  const composeId = compose.id;

  const save = async (service: string, enabled: boolean) => {
    setSaving(service);
    try {
      await setZeroDowntime(composeId, service, enabled);
      await onReload();
    } finally {
      setSaving(undefined);
    }
  };

  const turnOff = async (service: string) => {
    setSaveError(undefined);
    try {
      await save(service, false);
    } catch (reason: unknown) {
      setSaveError(errorText(reason, `Could not turn off zero-downtime deploy for ${service}`));
    }
  };

  const accept = async () => {
    if (!confirm) {
      return;
    }
    setConfirmError(undefined);
    try {
      await save(confirm.service, true);
      setConfirm(undefined);
    } catch (reason: unknown) {
      setConfirmError(errorText(reason, `Could not turn on zero-downtime deploy for ${confirm.service}`));
    }
  };

  const closeConfirm = () => {
    setConfirm(undefined);
    setConfirmError(undefined);
  };

  if (loadError && !services) {
    return (
      <Alert
        action={<Button onClick={() => void onReload()}>Retry</Button>}
        description={loadError}
        showIcon
        style={{ marginTop: LAYOUT.gutter }}
        title="Could not load the zero-downtime switches"
        type="error"
      />
    );
  }
  if (!services || services.length === 0) {
    return null;
  }

  return (
    <>
      <Card style={{ marginTop: LAYOUT.gutter }} title="Zero-downtime deploy" variant="outlined">
        <Paragraph type="secondary">
          New containers take traffic only once their healthcheck passes; the old one is then stopped. See the
          requirements under the YAML editor. Changing this Service&apos;s Domains makes the next deploy briefly
          unavailable.
        </Paragraph>
        {saveError && (
          <Alert
            closable={{ afterClose: () => setSaveError(undefined) }}
            showIcon
            style={{ marginBottom: LAYOUT.gutterSmall }}
            title={saveError}
            type="error"
          />
        )}
        <Flex gap={LAYOUT.gutterSmall} vertical>
          {services.map((row) => (
            <div key={row.service}>
              <Flex align="center" gap={LAYOUT.gutterSmall}>
                <Switch
                  aria-label={`Zero-downtime deploy for ${row.service}`}
                  checked={row.enabled}
                  loading={saving === row.service}
                  onChange={(checked) => (checked ? setConfirm(row) : void turnOff(row.service))}
                />
                <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{row.service}</Text>
              </Flex>
              {row.enabled && row.reasons.length > 0 && (
                <Alert
                  action={
                    <Button onClick={onOpenEditor} size="small" type="link">
                      Open editor
                    </Button>
                  }
                  description={<Reasons reasons={row.reasons} />}
                  showIcon
                  style={{ marginTop: LAYOUT.gutterSmall }}
                  title={`Deploys of ${row.service} will be refused until these are fixed`}
                  type="warning"
                />
              )}
            </div>
          ))}
        </Flex>
      </Card>
      <Modal
        cancelText="Cancel"
        confirmLoading={confirm !== undefined && saving === confirm.service}
        destroyOnHidden
        okText="Accept and turn on"
        onCancel={closeConfirm}
        onOk={() => void accept()}
        open={confirm !== undefined}
        title={confirm ? `Turn on zero-downtime deploy for ${confirm.service}?` : undefined}
      >
        <Paragraph>{confirm?.service} must meet each of these, or a deploy can drop requests:</Paragraph>
        <ZeroDowntimeRequirements />
        {confirm && confirm.reasons.length > 0 && (
          <Alert
            description={<Reasons reasons={confirm.reasons} />}
            showIcon
            style={{ marginTop: LAYOUT.gutter }}
            title="Deploys will be refused until these are fixed"
            type="warning"
          />
        )}
        {confirmError && (
          <Alert showIcon style={{ marginTop: LAYOUT.gutter }} title={confirmError} type="error" />
        )}
      </Modal>
    </>
  );
}
