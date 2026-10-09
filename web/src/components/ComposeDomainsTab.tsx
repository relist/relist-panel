import { DeleteOutlined, EditOutlined, PlusOutlined, RocketOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Popconfirm, Skeleton, Table, Tag, Typography } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import { ApiError } from '../data/api';
import { usePanel } from '../data/store';
import type { Compose, ComposeDomains, Domain, DomainInput } from '../data/types';
import { domainUrl, domainsPendingRedeploy, httpsTag } from '../pages/domains-view';
import { DomainFormModal } from './DomainFormModal';
import { EmptyState } from './primitives';

const { Text } = Typography;

/** A domain's Address: host plus path, e.g. `api.example.com/`. */
function address(domain: Domain) {
  return `${domain.host}${domain.path}`;
}

/**
 * The Compose screen's Domains tab (GUIDELINES §4.6). It loads its own list on
 * mount and after each mutation; the store refreshes the Compose's status so
 * the redeploy banner follows `domainsChangedAt`.
 */
export function ComposeDomainsTab({
  compose,
  deploying,
  onDeploy,
}: {
  compose: Compose;
  deploying: boolean;
  onDeploy: () => void;
}) {
  const { createDomain, deleteDomain, loadDomains, updateDomain } = usePanel();
  const [data, setData] = useState<{ composeId: number; payload?: ComposeDomains; error?: string; refreshing?: boolean }>();
  const [modal, setModal] = useState<{ domain?: Domain }>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<ApiError>();
  const [deleteError, setDeleteError] = useState<string>();
  const request = useRef(0);
  const composeId = compose.id;

  const fetchDomains = useCallback(async () => {
    const mine = ++request.current;
    // The previous payload stays valid for the same Compose: keep it on screen
    // while refetching. Another Compose starts from the skeleton.
    setData((previous) =>
      previous?.composeId === composeId && previous.payload
        ? { composeId, payload: previous.payload, refreshing: true }
        : { composeId },
    );
    try {
      const payload = await loadDomains(composeId);
      if (request.current === mine) {
        setData({ composeId, payload });
      }
    } catch (reason: unknown) {
      if (request.current === mine) {
        const error = reason instanceof Error ? reason.message : 'Could not load this Compose’s domains';
        // A failed refetch keeps the last good table; the error shows above it.
        setData((previous) =>
          previous?.composeId === composeId && previous.payload ? { composeId, error, payload: previous.payload } : { composeId, error },
        );
      }
    }
  }, [composeId, loadDomains]);

  useEffect(() => {
    void fetchDomains();
  }, [fetchDomains]);

  const current = data?.composeId === composeId ? data : undefined;
  const payload = current?.payload;

  const closeModal = () => {
    setModal(undefined);
    setSaveError(undefined);
  };

  const submit = async (input: DomainInput) => {
    if (!modal) {
      return;
    }
    setSaving(true);
    setSaveError(undefined);
    try {
      if (modal.domain) {
        await updateDomain(composeId, modal.domain.id, input);
      } else {
        await createDomain(composeId, input);
      }
      closeModal();
      void fetchDomains();
    } catch (reason: unknown) {
      setSaveError(reason instanceof ApiError ? reason : new ApiError(0, reason instanceof Error ? reason.message : 'Could not save the domain'));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (domain: Domain) => {
    setDeleteError(undefined);
    try {
      await deleteDomain(composeId, domain.id);
    } catch (reason: unknown) {
      setDeleteError(reason instanceof Error ? reason.message : 'Could not delete the domain');
    }
    void fetchDomains();
  };

  if (!current || (!payload && !current.error)) {
    return <Skeleton active paragraph={{ rows: 4 }} title={false} />;
  }

  if (!payload) {
    return (
      <Alert
        action={<Button onClick={() => void fetchDomains()}>Retry</Button>}
        description={current.error}
        title="Could not load this Compose’s domains"
        showIcon
        type="error"
      />
    );
  }

  const addButton = (
    <Button icon={<PlusOutlined />} onClick={() => setModal({})} type="primary">
      Add domain
    </Button>
  );

  return (
    <>
      {domainsPendingRedeploy(compose) && (
        <Alert
          action={
            <Button icon={<RocketOutlined />} loading={deploying} onClick={onDeploy} size="small" type="primary">
              Deploy
            </Button>
          }
          title="Domain changes apply on the next deploy."
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          type="info"
        />
      )}
      {current.error && (
        <Alert
          action={
            <Button onClick={() => void fetchDomains()} size="small">
              Retry
            </Button>
          }
          description={current.error}
          title="Could not refresh the domains"
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          type="error"
        />
      )}
      {deleteError && (
        <Alert
          closable={{ afterClose: () => setDeleteError(undefined) }}
          title={deleteError}
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          type="error"
        />
      )}
      <Card
        extra={payload.domains.length > 0 ? addButton : undefined}
        title="Domains"
        variant="outlined"
      >
        {payload.domains.length === 0 ? (
          <EmptyState action={addButton} title="No domains. Add one to route a Service through Traefik." />
        ) : (
          <Table<Domain>
            columns={[
              {
                dataIndex: 'host',
                render: (_, domain) => (
                  <a href={domainUrl(domain)} rel="noreferrer" target="_blank">
                    {address(domain)}
                  </a>
                ),
                title: 'Host and path',
              },
              {
                render: (_, domain) => (
                  <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>
                    {domain.service}:{domain.port}
                  </Text>
                ),
                title: 'Service',
              },
              { render: (_, domain) => <Tag>{httpsTag(domain)}</Tag>, title: 'HTTPS' },
              {
                align: 'right',
                render: (_, domain) => (
                  <Flex gap={4} justify="flex-end">
                    <Button
                      aria-label={`Edit ${address(domain)}`}
                      icon={<EditOutlined />}
                      onClick={() => setModal({ domain })}
                      size="small"
                      type="text"
                    />
                    <Popconfirm
                      okButtonProps={{ danger: true }}
                      okText="Delete"
                      onConfirm={() => remove(domain)}
                      title={`Delete ${address(domain)}?`}
                    >
                      <Button aria-label={`Delete ${address(domain)}`} danger icon={<DeleteOutlined />} size="small" type="text" />
                    </Popconfirm>
                  </Flex>
                ),
                title: 'Actions',
              },
            ]}
            dataSource={payload.domains}
            loading={current.refreshing}
            pagination={false}
            rowKey="id"
            scroll={{ x: 'max-content' }}
          />
        )}
      </Card>
      <DomainFormModal
        domain={modal?.domain}
        error={saveError}
        onCancel={closeModal}
        onSubmit={(input) => void submit(input)}
        open={modal !== undefined}
        saving={saving}
        services={payload.services}
      />
    </>
  );
}
