import { CloudServerOutlined } from '@ant-design/icons';
import { Alert, Flex, Tooltip, Typography, theme } from 'antd';
import { Link } from 'react-router';
import { paths } from '../app/paths';
import type { DiskPressure } from '../data/disk-pressure';
import { formatBytes } from './MetricChart';

const { Text } = Typography;

export function DiskPressureFooter({ pressure, onNavigate }: { pressure: DiskPressure; onNavigate?: () => void }) {
  const { token } = theme.useToken();
  const warning = pressure.kind === 'warning' || (pressure.kind === 'stale' && pressure.warning);
  const detail = 'percent' in pressure ? `${Number(pressure.percent.toFixed(1))}% used · ${formatBytes(pressure.freeBytes)} free` : undefined;
  const title = pressure.kind === 'loading' ? 'Waiting for disk sample…'
    : pressure.kind === 'unavailable' ? 'Disk usage unavailable'
    : pressure.kind === 'stale' ? 'Disk usage stale'
    : 'Panel data disk is nearly full';
  return (
    <footer aria-label="Docker host storage" style={{
      borderTop: `${token.lineWidth}px solid ${token.colorBorderSecondary}`,
      flexShrink: 0, minWidth: 0, overflowWrap: 'anywhere', padding: token.paddingSM,
    }}>
      <Flex align="center" gap={token.marginXS} style={{ marginBottom: pressure.kind === 'healthy' ? 0 : token.marginXS }}>
        <CloudServerOutlined style={{ color: token.colorTextTertiary }} />
        <Text style={{ fontSize: token.fontSizeSM }} type="secondary">Docker host</Text>
      </Flex>
      {pressure.kind !== 'healthy' && (
        <Alert
          showIcon
          title={title}
          type={warning ? 'warning' : 'info'}
          description={
            <Flex gap={token.marginXXS} vertical style={{ minWidth: 0 }}>
              {detail && <span>{pressure.kind === 'stale' ? 'Last reading: ' : ''}{detail}</span>}
              <span>Filesystem containing Panel and SQLite data.</span>
              {'reason' in pressure && <Tooltip title={pressure.reason}><span>{pressure.kind === 'stale' ? 'Current usage is unknown.' : 'Current reading is unavailable.'}</span></Tooltip>}
              <Link onClick={onNavigate} to={paths.monitoring}>Open Monitoring</Link>
            </Flex>
          }
          styles={{
            root: { padding: token.paddingXS, minWidth: 0 },
            icon: { fontSize: token.fontSize },
            title: { fontSize: token.fontSizeSM },
            description: { fontSize: token.fontSizeSM },
          }}
        />
      )}
    </footer>
  );
}
