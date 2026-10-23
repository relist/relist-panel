import { RocketOutlined } from '@ant-design/icons';
import { Alert, Button } from 'antd';

import { LAYOUT } from '../app/theme';
import type { Compose } from '../data/types';
import { redeployNotice } from '../pages/domains-view';

/**
 * The "changes apply on the next deploy" banner, shared by the tabs whose
 * edits only take effect on a deploy. Renders nothing when neither domains
 * nor Compose Variables changed since the last one.
 */
export function RedeployBanner({ compose, deploying, onDeploy }: { compose: Compose; deploying: boolean; onDeploy: () => void }) {
  const notice = redeployNotice(compose);
  if (!notice) {
    return null;
  }
  return (
    <Alert
      action={
        <Button icon={<RocketOutlined />} loading={deploying} onClick={onDeploy} size="small" type="primary">
          Deploy now
        </Button>
      }
      title={notice}
      showIcon
      style={{ marginBottom: LAYOUT.gutterSmall }}
      type="info"
    />
  );
}
