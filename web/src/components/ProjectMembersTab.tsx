import { DeleteOutlined, PlusOutlined, SaveOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Select, Skeleton, Space, Typography } from 'antd';
import { useEffect, useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { Grant, Role } from '../data/api';
import { GrantTree } from './GrantTree';
import type { GrantInput, GrantTarget } from './GrantTree';

const { Text } = Typography;

type Member = { userId: number; email: string; grants: GrantInput[] };

function membersOf(grants: Grant[]): Member[] {
  const members: Member[] = [];
  for (const grant of grants) {
    let member = members.find((candidate) => candidate.userId === grant.userId);
    if (!member) {
      member = { userId: grant.userId, email: grant.email, grants: [] };
      members.push(member);
    }
    member.grants.push({ projectId: grant.projectId, environmentId: grant.environmentId, role: grant.role });
  }
  return members;
}

/**
 * The Project's Members tab (admins): every User with a Grant here, each with
 * the same Grant tree as the User page, limited to this Project. Saving
 * replaces the Project's Grants in one call, so it never rewrites a User's
 * Grants on other Projects.
 */
export function ProjectMembersTab({ project }: { project: GrantTarget }) {
  const { message } = App.useApp();
  const grants = useSWR(`/api/projects/${project.id}/grants`, () => api.listProjectGrants(project.id));
  const users = useSWR('/api/users', () => api.listUsers());
  const [members, setMembers] = useState<Member[]>();
  const [adding, setAdding] = useState<number>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();

  useEffect(() => {
    if (grants.data && members === undefined) setMembers(membersOf(grants.data));
  }, [grants.data, members]);

  // Admins see every Project already; a Grant would change nothing for them.
  const candidates = (users.data ?? []).filter(
    (user) => !user.isAdmin && !members?.some((member) => member.userId === user.id),
  );

  const add = () => {
    const user = users.data?.find((candidate) => candidate.id === adding);
    if (!user || !members) return;
    setMembers([...members, { userId: user.id, email: user.email, grants: [{ projectId: project.id, environmentId: null, role: 'viewer' as Role }] }]);
    setAdding(undefined);
  };

  const save = async () => {
    if (!members) return;
    setSaving(true);
    setSaveError(undefined);
    try {
      await api.putProjectGrants(
        project.id,
        members.flatMap((member) => member.grants.map(({ environmentId, role }) => ({ userId: member.userId, environmentId, role }))),
      );
      await grants.mutate();
      void message.success(`Saved members of ${project.name}`);
    } catch (reason) {
      setSaveError(reason instanceof Error ? reason.message : 'Could not save members');
    } finally {
      setSaving(false);
    }
  };

  const loadError = grants.error ?? users.error;

  return (
    <Flex gap={16} vertical>
      {loadError && <Alert showIcon title={loadError instanceof Error ? loadError.message : 'Could not load members'} type="error" />}
      {saveError && <Alert showIcon title={saveError} type="error" />}
      <Flex align="center" gap={8} justify="space-between" wrap>
        <Space wrap>
          <Select
            aria-label="User to add"
            onChange={setAdding}
            options={candidates.map((user) => ({ label: user.email, value: user.id }))}
            placeholder="Choose a user"
            style={{ minWidth: 260 }}
            value={adding}
          />
          <Button disabled={adding === undefined} icon={<PlusOutlined />} onClick={add}>
            Add member
          </Button>
        </Space>
        <Button disabled={!members} icon={<SaveOutlined />} loading={saving} onClick={() => void save()} type="primary">
          Save members
        </Button>
      </Flex>
      {!members ? (
        <Skeleton active />
      ) : members.length === 0 ? (
        <Text type="secondary">Nobody has a Grant on this Project yet. Admins see it anyway.</Text>
      ) : (
        members.map((member) => (
          <Card
            extra={
              <Button
                aria-label={`Remove ${member.email}`}
                icon={<DeleteOutlined />}
                onClick={() => setMembers(members.filter((candidate) => candidate.userId !== member.userId))}
                size="small"
                type="text"
              />
            }
            key={member.userId}
            size="small"
            title={member.email}
          >
            <GrantTree
              onChange={(next) => setMembers(members.map((candidate) => (candidate.userId === member.userId ? { ...candidate, grants: next } : candidate)))}
              projects={[project]}
              value={member.grants}
            />
          </Card>
        ))
      )}
    </Flex>
  );
}
