import { CheckCircleOutlined, ToolOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Descriptions, Flex, Form, Input, Popconfirm, Radio, Tag, Typography } from 'antd';
import { useState } from 'react';
import { Link } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { ApiError, api } from '../data/api';
import type { RegistryCheck, RegistryTLS } from '../data/types';
import { CHECK_STEP, REGISTRY_STATUS } from '../pages/registry-view';
import { DangerModal } from './DangerModal';
import { formatBytes } from './MetricChart';
import { REGISTRY_KEY } from './RegistryOffState';

const { Text } = Typography;

/**
 * Settings: the Registry's hostname and TLS, on/off, the endpoint check, and
 * the danger zone that removes its volume (ADR-0033).
 */
export function RegistrySettingsTab() {
  const { data, error, isLoading, mutate } = useSWR(REGISTRY_KEY, () => api.getRegistry());
  const [form] = Form.useForm<{ hostname: string; tls: RegistryTLS }>();
  const [busy, setBusy] = useState<string>();
  const [failure, setFailure] = useState<string>();
  const [check, setCheck] = useState<RegistryCheck>();
  const [deleting, setDeleting] = useState(false);

  const run = async (name: string, action: () => Promise<unknown>) => {
    setBusy(name);
    setFailure(undefined);
    try {
      await action();
      return true;
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : `Could not ${name}`);
      return false;
    } finally {
      setBusy(undefined);
      void mutate();
    }
  };

  const save = async (values: { hostname: string; tls: RegistryTLS }) => {
    setBusy('save');
    setFailure(undefined);
    try {
      await mutate(api.saveRegistry({ hostname: values.hostname.trim(), tls: values.tls }), { revalidate: false });
    } catch (reason: unknown) {
      const message = reason instanceof Error ? reason.message : 'Could not save the Registry settings';
      if (reason instanceof ApiError && (reason.status === 409 || reason.status === 422) && /hostname|Domain|domain/.test(message)) {
        form.setFields([{ errors: [message], name: 'hostname' }]);
      } else {
        setFailure(message);
      }
    } finally {
      setBusy(undefined);
    }
  };

  if (!data) {
    return error ? (
      <Alert showIcon title={error instanceof Error ? error.message : 'Could not load the Registry'} type="error" />
    ) : (
      <Card loading={isLoading} variant="outlined" />
    );
  }

  const { settings } = data;
  const status = REGISTRY_STATUS[data.status];
  const using = data.composesUsing;

  return (
    <Flex gap={16} vertical>
      {failure && <Alert closable onClose={() => setFailure(undefined)} showIcon title={failure} type="error" />}
      <Card
        extra={
          settings.enabled ? (
            <Popconfirm
              description={
                using.length > 0
                  ? `${using.join(', ')} pull from this Registry. Their next deploy will fail to pull; running containers keep running.`
                  : 'Pushes and pulls stop. The data and Push credentials stay.'
              }
              okButtonProps={{ danger: true }}
              okText="Disable Registry"
              onConfirm={() => run('disable the Registry', () => api.disableRegistry())}
              title="Disable the Registry?"
            >
              <Button loading={busy === 'disable the Registry'}>Disable</Button>
            </Popconfirm>
          ) : (
            <Button
              loading={busy === 'enable the Registry'}
              onClick={() => void run('enable the Registry', () => api.enableRegistry())}
              type="primary"
            >
              Enable
            </Button>
          )
        }
        title="Registry"
        variant="outlined"
      >
        <Flex gap={16} vertical>
          <Flex align="center" gap={8} wrap>
            <Tag color={status.color}>{status.label}</Tag>
            {data.status === 'drift' && (
              <Button
                icon={<ToolOutlined />}
                loading={busy === 'repair the Registry'}
                onClick={() => void run('repair the Registry', () => api.repairRegistry())}
                size="small"
              >
                Repair
              </Button>
            )}
            <Text type="secondary">Disk used {formatBytes(data.diskBytes)}</Text>
          </Flex>
          {data.status === 'drift' && (
            <Text type="secondary">The container or its files no longer match these settings. Repair recreates them.</Text>
          )}
          {data.lastError && <Alert showIcon title={data.lastError} type="error" />}
          <Form
            form={form}
            initialValues={{ hostname: settings.hostname, tls: settings.tls || 'letsencrypt' }}
            layout="vertical"
            onFinish={(values) => void save(values)}
            style={{ maxWidth: 520 }}
          >
            <Form.Item
              extra="CI pushes to https://<hostname>. Point its DNS at this host. Changing it while enabled recreates the Registry."
              label="Hostname"
              name="hostname"
              rules={[{ message: 'Enter the hostname', required: true, whitespace: true }]}
            >
              <Input autoComplete="off" placeholder="registry.example.com" />
            </Form.Item>
            <Form.Item label="TLS" name="tls">
              <Radio.Group
                options={[
                  { label: "Let's Encrypt", value: 'letsencrypt' },
                  { label: 'Uploaded Certificate', value: 'certificate' },
                ]}
              />
            </Form.Item>
            <Button htmlType="submit" loading={busy === 'save'}>
              Save
            </Button>
          </Form>
        </Flex>
      </Card>

      <Card title="Endpoint" variant="outlined">
        <Descriptions
          column={1}
          items={[
            {
              children: settings.hostname ? <Text copyable>{`https://${settings.hostname}`}</Text> : <Text type="secondary">Not set</Text>,
              key: 'url',
              label: 'URL',
            },
            {
              children: (
                <Flex gap={4} vertical>
                  <Text code>{data.readTimeout || '3600s'}</Text>
                  <Text type="secondary">
                    Each pushed layer must arrive within it. Change it in <Link to={paths.traefik}>Traefik settings</Link>.
                  </Text>
                </Flex>
              ),
              key: 'timeout',
              label: 'Request read timeout',
            },
          ]}
          size="small"
        />
        <Flex align="center" gap={12} style={{ marginTop: 16 }} wrap>
          <Button
            disabled={!settings.enabled}
            icon={<CheckCircleOutlined />}
            loading={busy === 'check the endpoint'}
            onClick={() =>
              void run('check the endpoint', async () => {
                setCheck(undefined);
                setCheck(await api.checkRegistry());
              })
            }
          >
            Check endpoint
          </Button>
          {check &&
            (check.ok ? (
              <Text type="success">Reachable with the panel's login</Text>
            ) : (
              <Flex gap={2} vertical>
                <Text strong type="danger">
                  {check.step ? `${CHECK_STEP[check.step]} failed` : 'Check failed'}
                </Text>
                {check.error && <Text type="secondary">{check.error}</Text>}
              </Flex>
            ))}
        </Flex>
      </Card>

      <Card title="Danger zone" variant="outlined">
        <Flex align="center" gap={16} justify="space-between" wrap>
          <Text type="secondary">
            Removes the relist-registry volume: every repository and tag. Push credentials and settings stay.
            {settings.enabled && ' Disable the Registry first.'}
          </Text>
          <Button danger disabled={settings.enabled || !settings.hostname} onClick={() => setDeleting(true)}>
            Delete all registry data
          </Button>
        </Flex>
      </Card>
      <DangerModal
        confirmWord={settings.hostname}
        description="Every image pushed to this Registry is removed from the host. CI must push again before Composes using them can deploy."
        loading={busy === 'delete the registry data'}
        okText="Delete data"
        onCancel={() => setDeleting(false)}
        onConfirm={() =>
          void run('delete the registry data', () => api.deleteRegistryData(settings.hostname)).then((ok) => ok && setDeleting(false))
        }
        open={deleting}
        title="Delete all registry data?"
      />
    </Flex>
  );
}
