import { SaveOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Skeleton, Tag } from 'antd';
import { useEffect, useMemo, useState } from 'react';
import { useParams } from 'react-router';
import useSWR from 'swr';

import { GrantTree } from '../components/GrantTree';
import type { GrantInput, GrantTarget } from '../components/GrantTree';
import { PageHeader } from '../components/primitives';
import { api } from '../data/api';
import { usePanel } from '../data/store';

/**
 * One User's access (spec: UI → User detail): a role per Project, overridden
 * per Environment, saved in one go. An admin sees every Project, so their
 * tree is shown read-only.
 */
export function UserPage() {
  const { userId: userParam } = useParams();
  const userId = Number(userParam);
  const { message } = App.useApp();
  const { projects } = usePanel();
  const users = useSWR('/api/users', () => api.listUsers());
  const grants = useSWR(`/api/users/${userId}/grants`, () => api.listUserGrants(userId));
  const [draft, setDraft] = useState<GrantInput[]>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();

  useEffect(() => {
    if (grants.data && draft === undefined) {
      setDraft(grants.data.map(({ environmentId, projectId, role }) => ({ environmentId, projectId, role })));
    }
  }, [draft, grants.data]);

  const targets: GrantTarget[] = useMemo(
    () => projects.map((project) => ({
      id: project.id,
      name: project.name,
      environments: project.environments.map((environment) => ({ id: environment.id, name: environment.name })),
    })),
    [projects],
  );

  const user = users.data?.find((candidate) => candidate.id === userId);
  const loadError = users.error ?? grants.error;

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    setSaveError(undefined);
    try {
      await api.putUserGrants(userId, draft);
      await grants.mutate();
      void message.success(`Saved access for ${user?.email ?? 'the user'}`);
    } catch (reason) {
      setSaveError(reason instanceof Error ? reason.message : 'Could not save access');
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <PageHeader
        breadcrumb={[{ href: '/users', title: 'Users' }, { title: user?.email ?? 'User' }]}
        description="What this user may do in each Project. An Environment can raise, lower or hide the Project's role."
        extra={
          user && !user.isAdmin && (
            <Button disabled={!draft} icon={<SaveOutlined />} loading={saving} onClick={() => void save()} type="primary">
              Save access
            </Button>
          )
        }
        tags={user && (user.isOwner ? <Tag color="gold">Owner</Tag> : user.isAdmin ? <Tag color="blue">Admin</Tag> : <Tag>Member</Tag>)}
        title={user?.email ?? 'User'}
      />
      {loadError && (
        <Alert
          showIcon
          style={{ marginBottom: 16 }}
          title={loadError instanceof Error ? loadError.message : 'Could not load the user'}
          type="error"
        />
      )}
      {saveError && <Alert showIcon style={{ marginBottom: 16 }} title={saveError} type="error" />}
      {user?.isAdmin && <Alert showIcon style={{ marginBottom: 16 }} title="Admins see every Project." type="info" />}
      <Card variant="outlined">
        {draft && user ? (
          <GrantTree onChange={setDraft} projects={targets} readOnly={user.isAdmin} value={draft} />
        ) : (
          <Skeleton active />
        )}
      </Card>
    </>
  );
}
