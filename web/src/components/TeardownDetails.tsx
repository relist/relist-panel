import { Typography } from 'antd';

const { Paragraph } = Typography;

/** Shared note for every Project and Environment deletion: what a failed teardown leaves behind. */
export function TeardownDetails() {
  return (
    <Paragraph style={{ marginTop: 12 }} type="secondary">
      If teardown fails, the Project, Environment and Compose records are kept so you can retry.
      Child Composes already removed stay removed; their deleted volumes and files are not restored.
    </Paragraph>
  );
}
