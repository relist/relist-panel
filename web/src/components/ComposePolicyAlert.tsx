import { SafetyCertificateOutlined } from '@ant-design/icons';
import { Alert, Button, Typography } from 'antd';
import { useState } from 'react';

import type { ComposePolicy } from '../data/api';

const { Text } = Typography;

/**
 * What the Deploy button does under the Compose policy: deploy as usual,
 * wait for an approver, or approve and deploy. Unsaved edits are checked
 * when they are saved and queued, so they deploy as usual here.
 */
export function policyDeployState(policy: ComposePolicy | undefined, approver: boolean, dirty: boolean): 'deploy' | 'waiting' | 'approve' {
  if (!policy || dirty || policy.violations.length === 0 || policy.approved) return 'deploy';
  return approver && policy.configHash ? 'approve' : 'waiting';
}

/**
 * The Compose page's policy Alert (ADR-0032): the violations of the saved
 * config, an Approve button for an approver, and a note once it is approved.
 */
export function ComposePolicyAlert({
  approver,
  onApprove,
  policy,
}: {
  approver: boolean;
  onApprove: () => Promise<void>;
  policy: ComposePolicy | undefined;
}) {
  const [approving, setApproving] = useState(false);
  if (!policy || policy.violations.length === 0) return null;
  const list = (
    <ul style={{ margin: 0, paddingInlineStart: 20 }}>
      {policy.violations.map((violation, index) => (
        <li key={index}>
          <Text>{`${violation.service}: ${violation.message}`}</Text>
        </li>
      ))}
    </ul>
  );
  if (policy.approved) {
    return <Alert description={list} showIcon title="An approver approved this config" type="info" />;
  }
  const approve = async () => {
    setApproving(true);
    try {
      await onApprove();
    } finally {
      setApproving(false);
    }
  };
  return (
    <Alert
      action={
        approver && policy.configHash ? (
          <Button icon={<SafetyCertificateOutlined />} loading={approving} onClick={() => void approve()} size="small">
            Approve
          </Button>
        ) : undefined
      }
      description={
        <>
          {list}
          <Text type="secondary">
            {approver
              ? 'This config could reach the host. Approve it if you trust it, or change the Compose.'
              : 'This config could reach the host, so an approver must approve it before it can deploy.'}
          </Text>
        </>
      }
      showIcon
      title="This Compose needs approval"
      type="warning"
    />
  );
}
