import { AppstoreOutlined, DashboardOutlined, FileTextOutlined, FolderOutlined, RocketOutlined, SearchOutlined } from '@ant-design/icons';
import { Flex, Input, Modal, Tag, Typography, theme } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { navigate, paths } from '../app/router';
import { usePanel } from '../data/store';

const { Text } = Typography;

type Hit = {
  group: string;
  icon: ReactNode;
  key: string;
  meta?: ReactNode;
  title: string;
  to: string;
};

/**
 * ⌘K palette. The panel's domain is small and deeply nested, so the fastest
 * path to a Compose is usually typing its name rather than walking
 * Project → Environment → Compose.
 */
export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { projects } = usePanel();
  const { token } = theme.useToken();
  const [query, setQuery] = useState('');

  useEffect(() => {
    if (open) setQuery('');
  }, [open]);

  const hits = useMemo<Hit[]>(() => {
    const staticHits: Hit[] = [
      { group: 'Go to', icon: <AppstoreOutlined />, key: 'overview', title: 'Overview', to: paths.overview },
      { group: 'Go to', icon: <FolderOutlined />, key: 'projects', title: 'Projects', to: paths.projects },
      { group: 'Go to', icon: <DashboardOutlined />, key: 'monitoring', title: 'Monitoring', to: paths.monitoring },
      { group: 'Design', icon: <FileTextOutlined />, key: 'guidelines', title: 'Design guidelines', to: paths.guidelines },
      { group: 'Design', icon: <RocketOutlined />, key: 'states', title: 'States gallery', to: paths.states },
    ];
    const projectHits: Hit[] = projects.map((project) => ({
      group: 'Projects',
      icon: <FolderOutlined />,
      key: `project-${project.id}`,
      meta: `${project.environmentCount} environments`,
      title: project.name,
      to: paths.project(project.id),
    }));
    const composeHits: Hit[] = projects.flatMap((project) =>
      project.environments.flatMap((environment) =>
        environment.composes.map((compose) => ({
          group: 'Composes',
          icon: <RocketOutlined />,
          key: `compose-${compose.id}`,
          meta: environment.name,
          title: `${project.name} / ${compose.name}`,
          to: paths.compose(project.id, environment.id, compose.id),
        })),
      ),
    );
    const all = [...projectHits, ...composeHits, ...staticHits];
    if (!query.trim()) return all.slice(0, 7);
    const needle = query.toLowerCase();
    return all.filter((hit) => hit.title.toLowerCase().includes(needle) || hit.group.toLowerCase().includes(needle)).slice(0, 12);
  }, [projects, query]);

  const groups = useMemo(() => {
    const map = new Map<string, Hit[]>();
    for (const hit of hits) {
      map.set(hit.group, [...(map.get(hit.group) ?? []), hit]);
    }
    return [...map.entries()];
  }, [hits]);

  return (
    <Modal
      destroyOnHidden
      footer={null}
      onCancel={onClose}
      open={open}
      styles={{ body: { padding: 0 } }}
      title={null}
      width={560}
    >
      <Flex align="center" gap={8} style={{ borderBottom: `1px solid ${token.colorBorderSecondary}`, padding: '12px 16px' }}>
        <SearchOutlined style={{ color: token.colorTextTertiary }} />
        <Input
          autoFocus
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search Projects, Environments and Composes…"
          variant="borderless"
        />
        <Tag>Esc</Tag>
      </Flex>
      <div style={{ maxHeight: 420, overflowY: 'auto', padding: 8 }}>
        {groups.map(([group, groupHits]) => (
          <div key={group} style={{ marginBottom: 8 }}>
            <Text style={{ display: 'block', fontSize: 12, padding: '6px 8px' }} type="secondary">
              {group}
            </Text>
            {groupHits.map((hit) => (
              <Flex
                align="center"
                className="rp-hit"
                gap={10}
                key={hit.key}
                onClick={() => {
                  navigate(hit.to);
                  onClose();
                }}
                style={{ background: token.colorFillQuaternary, borderRadius: token.borderRadius, cursor: 'pointer', padding: '8px 10px' }}
              >
                <span style={{ color: token.colorTextTertiary }}>{hit.icon}</span>
                <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{hit.title}</span>
                {hit.meta && <Text type="secondary">{hit.meta}</Text>}
              </Flex>
            ))}
          </div>
        ))}
        {groups.length === 0 && (
          <div style={{ padding: 24, textAlign: 'center' }}>
            <Text type="secondary">No match for “{query}”.</Text>
          </div>
        )}
      </div>
    </Modal>
  );
}
