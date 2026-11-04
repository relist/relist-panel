import { Result } from 'antd';

/**
 * What a signed-in User who is not an admin sees in place of any screen but
 * their Account, until an admin gives them a Project (multi-user phase 2).
 */
export function NoAccessPage() {
  return <Result status="info" subTitle="An admin has not given you access to any Project." title="No access yet" />;
}
