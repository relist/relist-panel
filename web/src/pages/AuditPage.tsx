import { Flex } from 'antd';
import { useCallback } from 'react';
import useSWR from 'swr';

import { AuditTable } from '../components/AuditTable';
import { RetentionCard } from '../components/RetentionCard';
import { PageHeader } from '../components/primitives';
import { api } from '../data/api';
import type { AuditQuery } from '../data/api';

/**
 * The Audit log (admins): every change, refusal, sensitive read and sign-in,
 * newest first, and how long events are kept (ADR-0031).
 */
export function AuditPage() {
  const settings = useSWR('/api/audit/settings', () => api.getAuditSettings());
  const load = useCallback((query: AuditQuery) => api.listAudit(query), []);
  return (
    <>
      <PageHeader
        description="Who changed what, and when. Variable values are never recorded. An admin with access to the host can still edit panel.db."
        title="Audit log"
      />
      <Flex gap={24} vertical>
        <AuditTable load={load} />
        {settings.data && (
          <RetentionCard
            keepForever={settings.data.keepForever}
            max={36500}
            onSave={(input) => api.putAuditSettings(input)}
            onSaved={() => void settings.mutate()}
            retentionDays={settings.data.retentionDays}
          />
        )}
      </Flex>
    </>
  );
}
