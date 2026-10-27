import { Flex, Tag } from 'antd';
import { Link } from 'react-router';

import type { SummaryBadge } from '../pages/vulnerabilities-view';

/**
 * Critical / High / fixable counts as tags, linked to the Vulnerabilities page
 * when `to` is given. Nothing renders for a null badge: scanning off is not
 * "0 CVEs".
 */
export function VulnBadges({ badge, to }: { badge: SummaryBadge; to?: string }) {
  if (!badge) {
    return null;
  }
  const tags = (
    <Flex gap={4} wrap>
      <Tag color={badge.critical > 0 ? 'error' : undefined} variant="filled">{`${badge.critical} Critical`}</Tag>
      <Tag color={badge.high > 0 ? 'warning' : undefined} variant="filled">{`${badge.high} High`}</Tag>
      <Tag variant="outlined">{`${badge.fixable} fixable`}</Tag>
    </Flex>
  );
  return to ? (
    <Link aria-label={`${badge.critical} Critical, ${badge.high} High, ${badge.fixable} fixable vulnerabilities`} to={to}>
      {tags}
    </Link>
  ) : (
    tags
  );
}
