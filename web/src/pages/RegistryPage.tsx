import { ReloadOutlined } from '@ant-design/icons';
import { Button, Tabs } from 'antd';
import { useSearchParams } from 'react-router';
import { useSWRConfig } from 'swr';

import { ExternalRegistriesTab } from '../components/ExternalRegistriesTab';
import { PageHeader } from '../components/primitives';
import { RegistryCredentialsTab } from '../components/RegistryCredentialsTab';
import { RegistryRepositoriesTab } from '../components/RegistryRepositoriesTab';
import { RegistrySettingsTab } from '../components/RegistrySettingsTab';

const TABS = ['repositories', 'credentials', 'external', 'settings'] as const;
type TabKey = (typeof TABS)[number];

/**
 * Registry: this host's own image registry (ADR-0033) and the logins for
 * registries it does not run (ADR-0034). The tab is kept in
 * `?tab=` so a link opens the right one.
 */
export function RegistryPage() {
  const [params, setParams] = useSearchParams();
  const { mutate } = useSWRConfig();
  const asked = params.get('tab');
  const tab: TabKey = TABS.find((key) => key === asked) ?? 'repositories';

  return (
    <>
      <PageHeader
        description="The Registry CI pushes images to, and logins for the registries Composes pull private images from."
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void mutate((key) => typeof key === 'string' && key.startsWith('/api/registry'))}>
            Refresh
          </Button>
        }
        title="Registry"
      />
      <Tabs
        activeKey={tab}
        items={[
          { children: <RegistryRepositoriesTab />, key: 'repositories', label: 'Repositories' },
          { children: <RegistryCredentialsTab />, key: 'credentials', label: 'Push credentials' },
          { children: <ExternalRegistriesTab />, key: 'external', label: 'External registries' },
          { children: <RegistrySettingsTab />, key: 'settings', label: 'Settings' },
        ]}
        onChange={(key) => {
          setParams({ tab: key }, { replace: true });
          // The pane stays mounted while hidden: what CI pushed meanwhile
          // shows when the operator comes back to it.
          if (key === 'repositories') {
            void mutate((swrKey) => typeof swrKey === 'string' && (swrKey.startsWith('/api/registry/repositories') || swrKey.startsWith('/api/registry/tags')));
          }
        }}
      />
    </>
  );
}
