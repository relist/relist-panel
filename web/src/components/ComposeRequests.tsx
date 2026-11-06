import { Flex, Select, Typography } from 'antd';
import { useCallback, useState } from 'react';

import { api } from '../data/api';
import { usePanel } from '../data/store';
import type { Compose, RequestWindow } from '../data/types';
import { findComposeLocation } from '../data/types';
import { RequestMetricsPanel } from './RequestMetricsPanel';

const { Text, Title } = Typography;

const ALL_HOSTS = '';

/**
 * The Compose's request metrics, split by host (ADR-0033). The host list comes
 * with the answer, so the panel loads first and the section stays hidden until
 * the Compose turns out to have a Domain.
 */
export function ComposeRequests({ compose }: { compose: Compose }) {
  const { projects } = usePanel();
  const [host, setHost] = useState(ALL_HOSTS);
  const [hosts, setHosts] = useState<string[]>([]);

  const load = useCallback(
    async (window: RequestWindow) => {
      const location = findComposeLocation(projects, compose.id);
      if (!location) throw new Error('Compose not found');
      const result = await api.composeRequestMetrics(
        { projectId: location.project.id, environmentId: location.environment.id, composeId: compose.id },
        window,
        host || undefined,
      );
      setHosts(result.hosts ?? []);
      return result;
    },
    [compose.id, host, projects],
  );

  return (
    <div hidden={hosts.length === 0} style={{ marginTop: 24 }}>
      <Title level={5} style={{ marginTop: 0 }}>
        Requests
      </Title>
      <RequestMetricsPanel
        extra={
          <Flex align="center" gap={8}>
            <Text type="secondary">Host</Text>
            <Select
              aria-label="Host"
              onChange={setHost}
              options={[{ label: 'All hosts', value: ALL_HOSTS }, ...hosts.map((value) => ({ label: value, value }))]}
              style={{ minWidth: 200 }}
              virtual={false}
              value={host}
            />
          </Flex>
        }
        load={load}
        swrKey={`compose-requests:${compose.id}:${host}`}
      />
    </div>
  );
}
