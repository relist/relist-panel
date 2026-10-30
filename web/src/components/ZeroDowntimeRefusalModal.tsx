import { Button, Modal, Typography } from 'antd';

const { Paragraph } = Typography;

/**
 * A deploy the API refused because zero-downtime Services can't be rolled out
 * (ADR-0023): one line per Service and reason, and the way to the fix. The
 * Compose page offers its Editor and Domains tabs; a page listing many
 * Composes links to the refused one instead.
 */
export function ZeroDowntimeRefusalModal({
  compose,
  onClose,
  onOpenDomains,
  onOpenEditor,
  problems,
}: {
  compose?: { name: string; onOpen: () => void };
  onClose: () => void;
  onOpenDomains?: () => void;
  onOpenEditor?: () => void;
  /** Open while set. */
  problems: { service: string; reasons: string[] }[] | undefined;
}) {
  return (
    // The footer's Close is the one close control; a second icon with the
    // same name would leave a screen reader two "Close" buttons. Esc still works.
    <Modal
      closable={false}
      footer={
        <>
          {onOpenEditor && <Button onClick={onOpenEditor}>Open editor</Button>}
          {onOpenDomains && <Button onClick={onOpenDomains}>Open domains</Button>}
          {compose && <Button onClick={compose.onOpen}>{`Open ${compose.name}`}</Button>}
          <Button onClick={onClose}>Close</Button>
        </>
      }
      onCancel={onClose}
      open={problems !== undefined}
      title="Can't deploy with zero downtime"
    >
      <ul style={{ margin: 0, paddingInlineStart: 20 }}>
        {problems?.flatMap(({ service, reasons }) =>
          reasons.map((reason) => <li key={`${service} ${reason}`}>{`${service} — ${reason}`}</li>),
        )}
      </ul>
      <Paragraph style={{ marginBottom: 0, marginTop: 8 }} type="secondary">
        Nothing was queued.
      </Paragraph>
    </Modal>
  );
}
