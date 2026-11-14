import { PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Divider, Form, Input, InputNumber, Modal, Select, Switch } from 'antd';
import { useEffect, useState } from 'react';
import useSWR from 'swr';
import { api } from '../data/api';
import type { ApiError } from '../data/api';
import { useIsAdmin } from '../data/useCan';
import type { AuthGroupOption, Domain, DomainInput } from '../data/types';
import { domainFormValues, domainInput } from '../pages/domains-view';
import type { DomainFormValues } from '../pages/domains-view';
import { NewAuthGroupModal } from './NewAuthGroupModal';

const AUTH_OPTIONS_KEY = '/api/auth-group-options';
/** The Select's value for None; the form stores `null`. */
const NO_GROUP = 0;

const FIELDS: readonly (keyof DomainFormValues)[] = [
  'host',
  'path',
  'internalPath',
  'stripPath',
  'service',
  'port',
  'entrypoint',
  'https',
  'certProvider',
  'certResolver',
  'middlewares',
  'authGroupId',
];

/**
 * The Add / Edit Domain modal (GUIDELINES §4.6). Hidden fields are cleared by
 * `domainInput` on submit. A server error naming a field lands on that field;
 * a 409, or a 400 with no field, shows above the form.
 */
export function DomainFormModal({
  domain,
  error,
  onCancel,
  onSubmit,
  open,
  saving,
  services,
}: {
  open: boolean;
  domain?: Domain;
  services: string[];
  saving: boolean;
  error?: ApiError;
  onCancel: () => void;
  onSubmit: (input: DomainInput) => void;
}) {
  const [form] = Form.useForm<DomainFormValues>();
  const https = Form.useWatch('https', form);
  const certProvider = Form.useWatch('certProvider', form);
  const customEntrypoint = Form.useWatch('customEntrypoint', form);
  const isAdmin = useIsAdmin();
  const { data: groups, mutate: reloadGroups } = useSWR(open ? AUTH_OPTIONS_KEY : null, () => api.listAuthGroupOptions());
  const [creatingGroup, setCreatingGroup] = useState(false);

  // A stored group the list does not hold (still loading) keeps its name.
  const groupOptions: AuthGroupOption[] = [...(groups ?? [])];
  if (domain?.authGroupId != null && !groupOptions.some((group) => group.id === domain.authGroupId)) {
    groupOptions.push({ id: domain.authGroupId, name: domain.authGroupName || String(domain.authGroupId) });
  }

  // Each opening starts from the domain being edited, or the defaults.
  useEffect(() => {
    if (open) {
      form.resetFields();
      form.setFieldsValue(domainFormValues(domain));
    }
  }, [open, domain, form]);

  const fieldError = FIELDS.find((name) => name === error?.field);
  useEffect(() => {
    if (error && fieldError) {
      form.setFields([{ errors: [error.message], name: fieldError }]);
    }
  }, [error, fieldError, form]);

  return (
    <Modal
      cancelText="Cancel"
      confirmLoading={saving}
      destroyOnHidden
      okText={domain ? 'Save' : 'Add domain'}
      onCancel={onCancel}
      onOk={() => form.submit()}
      open={open}
      title={domain ? 'Edit domain' : 'Add domain'}
      width={560}
    >
      {error && !fieldError && (
        <Alert showIcon style={{ marginBottom: 16 }} title={error.message} type="error" />
      )}
      <Form<DomainFormValues>
        autoComplete="off"
        form={form}
        initialValues={domainFormValues(domain)}
        layout="vertical"
        onFinish={(values) => onSubmit(domainInput(values))}
        preserve={false}
      >
        <Form.Item label="Host" name="host" rules={[{ message: 'Enter a host', required: true, whitespace: true }]}>
          <Input placeholder="app.example.com" />
        </Form.Item>
        <Form.Item label="Path" name="path">
          <Input placeholder="/" />
        </Form.Item>
        <Form.Item
          extra="The path your application expects to receive requests on."
          label="Internal Path"
          name="internalPath"
        >
          <Input placeholder="/" />
        </Form.Item>
        <Form.Item
          extra="Remove the external path before forwarding."
          label="Strip Path"
          name="stripPath"
          valuePropName="checked"
        >
          <Switch />
        </Form.Item>
        <Form.Item
          extra={
            services.length === 0
              ? 'The saved YAML declares no Services that can take a domain.'
              : undefined
          }
          label="Service"
          name="service"
          rules={[{ message: 'Choose a Service', required: true }]}
        >
          <Select options={services.map((name) => ({ label: name, value: name }))} placeholder="Choose a Service" />
        </Form.Item>
        <Form.Item
          label="Container Port"
          name="port"
          rules={[{ message: 'Enter the container port', required: true, type: 'number' }]}
        >
          <InputNumber max={65535} min={1} precision={0} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item
          extra="Leave off to use web / websecure with an automatic HTTP→HTTPS redirect."
          label="Custom Entrypoint"
          name="customEntrypoint"
          valuePropName="checked"
        >
          <Switch />
        </Form.Item>
        {customEntrypoint && (
          <Form.Item
            label="Entrypoint"
            name="entrypoint"
            rules={[{ message: 'Enter an entrypoint', required: true, whitespace: true }]}
          >
            <Input placeholder="e.g. web-internal" />
          </Form.Item>
        )}
        <Form.Item label="HTTPS" name="https" valuePropName="checked">
          <Switch />
        </Form.Item>
        {https && (
          <Form.Item
            extra={
              certProvider === 'none'
                ? 'Traefik serves an uploaded certificate (Certificates page) that covers this host, otherwise its default self-signed certificate.'
                : undefined
            }
            label="Certificate Provider"
            name="certProvider"
          >
            <Select
              options={[
                { label: 'None', value: 'none' },
                { label: "Let's Encrypt", value: 'letsencrypt' },
                { label: 'Custom', value: 'custom' },
              ]}
            />
          </Form.Item>
        )}
        {https && certProvider === 'custom' && (
          <Form.Item
            extra="Must match a certificate resolver in Traefik's static config. The panel can't check it — a wrong name shows only in Traefik's log."
            label="Resolver name"
            name="certResolver"
            rules={[{ message: 'Enter a resolver name', required: true, whitespace: true }]}
          >
            <Input />
          </Form.Item>
        )}
        <Form.Item label="Middlewares" name="middlewares">
          <Select mode="tags" open={false} placeholder="e.g. rate-limit@file, auth@file" tokenSeparators={[',', ' ']} />
        </Form.Item>
        <Form.Item
          extra="Visitors must sign in with a user of this group. Applies on the next deploy; edits to the group apply immediately."
          getValueProps={(value: number | null) => ({ value: value ?? NO_GROUP })}
          label="Basic auth"
          name="authGroupId"
          normalize={(value: number) => (value === NO_GROUP ? null : value)}
        >
          <Select
            options={[
              { label: 'None', value: NO_GROUP },
              ...groupOptions.map((group) => ({ label: group.name, value: group.id })),
            ]}
            popupRender={(menu) => (
              <>
                {menu}
                {isAdmin && (
                  <>
                    <Divider style={{ margin: '4px 0' }} />
                    <Button block icon={<PlusOutlined />} onClick={() => setCreatingGroup(true)} type="text">
                      New group
                    </Button>
                  </>
                )}
              </>
            )}
          />
        </Form.Item>
      </Form>
      <NewAuthGroupModal
        onCancel={() => setCreatingGroup(false)}
        onCreated={(group) => {
          setCreatingGroup(false);
          void reloadGroups((current) => [...(current ?? []), group].sort((a, b) => a.name.localeCompare(b.name)), { revalidate: true });
          form.setFieldValue('authGroupId', group.id);
        }}
        open={creatingGroup}
      />
    </Modal>
  );
}
