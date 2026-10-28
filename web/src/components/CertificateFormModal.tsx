import { Alert, Form, Input, Modal } from 'antd';
import { useEffect } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import type { ApiError } from '../data/api';
import type { Certificate, CertificateInput } from '../data/types';

const FIELDS: readonly (keyof CertificateInput)[] = ['name', 'certificate', 'privateKey'];
const PEM_STYLE = { fontFamily: CODE_FONT_STACK, fontSize: 12 };

/**
 * The Add / Replace certificate modal. With `certificate` it replaces that
 * one's PEM pair in place: the name is shown but not sent. A server error
 * naming a field lands on that field; any other shows above the form.
 */
export function CertificateFormModal({
  certificate,
  error,
  onCancel,
  onSubmit,
  open,
  saving,
}: {
  open: boolean;
  certificate?: Certificate;
  saving: boolean;
  error?: ApiError;
  onCancel: () => void;
  onSubmit: (input: CertificateInput) => void;
}) {
  const [form] = Form.useForm<CertificateInput>();
  const replacing = certificate !== undefined;

  useEffect(() => {
    if (open) {
      form.resetFields();
      form.setFieldsValue({ certificate: '', name: certificate?.name ?? '', privateKey: '' });
    }
  }, [open, certificate, form]);

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
      okText={replacing ? 'Replace' : 'Create'}
      onCancel={onCancel}
      onOk={() => form.submit()}
      open={open}
      title={replacing ? 'Replace certificate' : 'Add certificate'}
      width={640}
    >
      {error && !fieldError && <Alert showIcon style={{ marginBottom: 16 }} title={error.message} type="error" />}
      <Form<CertificateInput>
        autoComplete="off"
        form={form}
        layout="vertical"
        onFinish={(values) => onSubmit({ ...values, name: values.name.trim() })}
        preserve={false}
      >
        <Form.Item
          label="Certificate Name"
          name="name"
          rules={[{ message: 'Enter a name', required: !replacing, whitespace: true }]}
        >
          <Input disabled={replacing} placeholder="My Certificate" />
        </Form.Item>
        <Form.Item
          label="Certificate Data"
          name="certificate"
          rules={[{ message: 'Paste the certificate', required: true, whitespace: true }]}
        >
          <Input.TextArea
            autoSize={{ maxRows: 12, minRows: 5 }}
            placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
            spellCheck={false}
            style={PEM_STYLE}
          />
        </Form.Item>
        <Form.Item
          label="Private Key"
          name="privateKey"
          rules={[{ message: 'Paste the private key', required: true, whitespace: true }]}
        >
          <Input.TextArea
            autoSize={{ maxRows: 12, minRows: 5 }}
            placeholder={'-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----'}
            spellCheck={false}
            style={PEM_STYLE}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
