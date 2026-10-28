import { DeleteOutlined, PlusOutlined, ReloadOutlined, SwapOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Popconfirm, Table, Tag, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { CertificateFormModal } from '../components/CertificateFormModal';
import { EmptyState, PageHeader } from '../components/primitives';
import { ApiError, api } from '../data/api';
import type { Certificate, CertificateInput } from '../data/types';
import { expiryState } from './certificates-view';

const { Text } = Typography;

const CERTIFICATES_KEY = '/api/certificates';
const DESCRIPTION =
  'Traefik serves an uploaded certificate to any HTTPS domain set to provider None whose host it covers. Changes apply immediately.';

type Dialog = { kind: 'add' } | { kind: 'replace'; certificate: Certificate } | undefined;

function asApiError(reason: unknown): ApiError {
  if (reason instanceof ApiError) return reason;
  return new ApiError(0, reason instanceof Error ? reason.message : 'Something went wrong');
}

/**
 * Certificates: the host-wide TLS certificates handed to Traefik's file
 * provider (ADR-0022). The panel never links one to a Domain; Traefik picks by
 * SNI. The private key never leaves the host.
 */
export function CertificatesPage() {
  const { message } = App.useApp();
  const { data, error, isLoading, mutate } = useSWR(CERTIFICATES_KEY, () => api.listCertificates());
  const [dialog, setDialog] = useState<Dialog>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<ApiError>();

  const reload = () => void mutate();
  const openAdd = () => {
    setSaveError(undefined);
    setDialog({ kind: 'add' });
  };
  const close = () => {
    setSaveError(undefined);
    setDialog(undefined);
  };

  const submit = async (input: CertificateInput) => {
    setSaving(true);
    setSaveError(undefined);
    try {
      if (dialog?.kind === 'replace') {
        await api.replaceCertificate(dialog.certificate.id, {
          certificate: input.certificate,
          privateKey: input.privateKey,
        });
      } else {
        await api.createCertificate(input);
      }
      close();
      reload();
    } catch (reason: unknown) {
      setSaveError(asApiError(reason));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (certificate: Certificate) => {
    try {
      await api.deleteCertificate(certificate.id);
      reload();
    } catch (reason: unknown) {
      void message.error(asApiError(reason).message);
    }
  };

  const now = new Date();
  const columns: TableColumnsType<Certificate> = [
    { dataIndex: 'name', key: 'name', render: (name: string) => <Text strong>{name}</Text>, title: 'Name' },
    {
      dataIndex: 'sans',
      key: 'sans',
      render: (sans: string[]) => (
        <Flex gap={4} wrap>
          {sans.map((san) => (
            <Tag key={san}>{san}</Tag>
          ))}
        </Flex>
      ),
      title: 'Domains',
    },
    { dataIndex: 'issuer', key: 'issuer', title: 'Issuer' },
    {
      dataIndex: 'notAfter',
      key: 'notAfter',
      render: (notAfter: string) => {
        const state = expiryState(notAfter, now);
        const date = notAfter.slice(0, 10);
        return (
          <Flex align="center" gap={8}>
            <Text type={state === 'ok' ? undefined : 'danger'}>{date}</Text>
            {state === 'expired' && <Tag color="error">Expired</Tag>}
          </Flex>
        );
      },
      title: 'Expires',
    },
    {
      align: 'right',
      key: 'actions',
      render: (_, certificate) => (
        <Flex gap={4} justify="flex-end">
          <Button
            aria-label={`Replace ${certificate.name}`}
            icon={<SwapOutlined />}
            onClick={() => {
              setSaveError(undefined);
              setDialog({ certificate, kind: 'replace' });
            }}
            type="text"
          />
          <Popconfirm
            description="Domains it covers fall back to Traefik's default certificate."
            okButtonProps={{ danger: true }}
            okText="Delete"
            onConfirm={() => remove(certificate)}
            title={`Delete certificate ${certificate.name}?`}
          >
            <Button aria-label={`Delete ${certificate.name}`} danger icon={<DeleteOutlined />} type="text" />
          </Popconfirm>
        </Flex>
      ),
      title: '',
    },
  ];

  return (
    <>
      <PageHeader
        description={DESCRIPTION}
        extra={
          <Flex gap={8}>
            <Button icon={<ReloadOutlined />} loading={isLoading} onClick={reload}>
              Refresh
            </Button>
            <Button icon={<PlusOutlined />} onClick={openAdd} type="primary">
              Add certificate
            </Button>
          </Flex>
        }
        title="Certificates"
      />

      {error && (
        <Alert
          action={<Button onClick={reload}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={error instanceof Error ? error.message : 'Could not load certificates'}
          type="error"
        />
      )}

      <Card variant="outlined">
        <Table<Certificate>
          columns={columns}
          dataSource={data ?? []}
          loading={isLoading}
          locale={{
            emptyText:
              isLoading || error ? (
                ' '
              ) : (
                <EmptyState
                  action={
                    <Button icon={<PlusOutlined />} onClick={openAdd} type="primary">
                      Add certificate
                    </Button>
                  }
                  description="Upload a certificate and its private key, then set a domain's provider to None."
                  title="No certificates yet"
                />
              ),
          }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>

      <CertificateFormModal
        certificate={dialog?.kind === 'replace' ? dialog.certificate : undefined}
        error={saveError}
        onCancel={close}
        onSubmit={(input) => void submit(input)}
        open={dialog !== undefined}
        saving={saving}
      />
    </>
  );
}
