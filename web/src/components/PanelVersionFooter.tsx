import { Typography, theme } from 'antd';
import { Link } from 'react-router';
import { paths } from '../app/paths';

const { Text } = Typography;

/** The running panel version under the sider, linking to Panel update. Nothing until it is known. */
export function PanelVersionFooter({ version, onNavigate }: { version: string | undefined; onNavigate?: () => void }) {
  const { token } = theme.useToken();
  if (!version) return null;
  return (
    <div style={{ flexShrink: 0, minWidth: 0, overflowWrap: 'anywhere', padding: `0 ${token.paddingSM}px ${token.paddingSM}px` }}>
      <Link onClick={onNavigate} to={paths.panelUpdate}>
        <Text style={{ fontSize: token.fontSizeSM }} type="secondary">Relist Panel {version}</Text>
      </Link>
    </div>
  );
}
