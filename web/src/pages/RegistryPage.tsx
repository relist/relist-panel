import { ReloadOutlined } from '@ant-design/icons';
import { Button, Tabs } from 'antd';
import { useSearchParams } from 'react-router';
import { useSWRConfig } from 'swr';

import { EXTERNAL_REGISTRIES_KEY, ExternalRegistriesTab } from '../components/ExternalRegistriesTab';
import { PageHeader } from '../components/primitives';

const TABS = ['external'] as const;
type TabKey = (typeof TABS)[number];

/**
 * Registry: where this host pulls private images from. The tab is kept in
 * `?tab=` so a link opens the right one.
 */
export function RegistryPage() {
  const [params, setParams] = useSearchParams();
  const { mutate } = useSWRConfig();
  const asked = params.get('tab');
  const tab: TabKey = TABS.find((key) => key === asked) ?? 'external';

  return (
    <>
      <PageHeader
        description="Logins for the registries Composes pull private images from."
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void mutate(EXTERNAL_REGISTRIES_KEY)}>
            Refresh
          </Button>
        }
        title="Registry"
      />
      <Tabs
        activeKey={tab}
        items={[{ children: <ExternalRegistriesTab />, key: 'external', label: 'External registries' }]}
        onChange={(key) => setParams({ tab: key }, { replace: true })}
      />
    </>
  );
}
