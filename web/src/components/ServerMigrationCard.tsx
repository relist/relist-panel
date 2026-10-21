import { Card, Flex, Typography } from 'antd';

const { Paragraph, Text } = Typography;

/**
 * Where a server Migration starts (ADR-0018, ADR-0019): it is driven from a
 * terminal on each host, so a signed-in web session cannot start one. The
 * card only names the commands.
 */
export function ServerMigrationCard() {
  return (
    <Card title="Server migration">
      <Flex gap={12} vertical>
        <Paragraph style={{ margin: 0 }} type="secondary">
          To move everything this panel manages to another server, install an empty panel there with the same
          RELIST_DATA_DIR. Then run the first command on this server to create a migration token, and the second on the
          new server to pull everything. Nothing here stops until the new server starts the migration.
        </Paragraph>
        <Flex gap={4} vertical>
          <Text type="secondary">On this server</Text>
          <Text code copyable>
            docker compose exec panel relist-panel migration serve
          </Text>
        </Flex>
        <Flex gap={4} vertical>
          <Text type="secondary">On the new server</Text>
          <Text code copyable>
            docker compose exec panel relist-panel migration pull
          </Text>
        </Flex>
      </Flex>
    </Card>
  );
}
