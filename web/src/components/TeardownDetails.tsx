import { Typography } from 'antd';

const { Paragraph, Text } = Typography;

/** Shared disclosure for every Project and Environment deletion entry. */
export function TeardownDetails({ directories }: { directories: string[] }) {
  return (
    <>
      {directories.length > 0 ? (
        <>
          <Paragraph style={{ marginTop: 12 }}>Deployment directories removed ({directories.length}):</Paragraph>
          <ul>
            {directories.map((directory, index) => (
              <li key={`${index}:${directory}`}><Text code>{directory}</Text></li>
            ))}
          </ul>
        </>
      ) : (
        <Paragraph>There are no Composes to tear down.</Paragraph>
      )}
      <Paragraph type="secondary">
        If teardown fails, the Project, Environment and Compose records are kept so you can retry.
        Child stacks already removed stay removed; their deleted volumes and files are not restored.
      </Paragraph>
    </>
  );
}
