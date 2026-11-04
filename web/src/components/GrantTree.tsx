import { Select, Table, Typography } from 'antd';
import type { TableColumnsType } from 'antd';

import type { Role } from '../data/api';

const { Text } = Typography;

/** One Grant as the tree edits it: on the Project when `environmentId` is null. */
export type GrantInput = { projectId: number; environmentId: number | null; role: Role };

/** The Projects and Environments the tree lists. */
export type GrantTarget = { id: number; name: string; environments: { id: number; name: string }[] };

const ROLE_OPTIONS = [
  { label: 'Viewer', value: 'viewer' },
  { label: 'Operator', value: 'operator' },
  { label: 'Editor', value: 'editor' },
];

// Select needs a string value; '' stands for "no Grant of its own".
const PROJECT_OPTIONS = [{ label: 'No grant', value: '' }, ...ROLE_OPTIONS];
const ENVIRONMENT_OPTIONS = [{ label: 'Same as Project', value: '' }, { label: 'No access', value: 'none' }, ...ROLE_OPTIONS];

type Row = {
  key: string;
  name: string;
  label: string;
  projectId: number;
  environmentId: number | null;
  children?: Row[];
};

/**
 * Orders Grants as the tree lists them: per Project, its Project Grant, then
 * its Environments' in their order. Grants on targets the tree does not list
 * follow, untouched.
 */
function ordered(value: GrantInput[], projects: GrantTarget[]): GrantInput[] {
  const out: GrantInput[] = [];
  const placed = new Set<GrantInput>();
  for (const project of projects) {
    for (const environmentId of [null, ...project.environments.map((environment) => environment.id)]) {
      const grant = value.find((candidate) => candidate.projectId === project.id && candidate.environmentId === environmentId);
      if (grant) {
        out.push(grant);
        placed.add(grant);
      }
    }
  }
  return [...out, ...value.filter((grant) => !placed.has(grant))];
}

/**
 * A User's Grants as a tree of Projects and their Environments, one role
 * Select per row (spec: UI → User detail). An Environment row may also be
 * "Same as Project", which drops its own Grant, or "No access", which hides
 * it. A Project row offers no No access: that is an Environment's alone.
 */
export function GrantTree({
  onChange,
  projects,
  readOnly = false,
  value,
}: {
  onChange: (value: GrantInput[]) => void;
  projects: GrantTarget[];
  readOnly?: boolean;
  value: GrantInput[];
}) {
  const roleOf = (projectId: number, environmentId: number | null) =>
    value.find((grant) => grant.projectId === projectId && grant.environmentId === environmentId)?.role ?? '';

  const set = (projectId: number, environmentId: number | null, role: string) => {
    const rest = value.filter((grant) => !(grant.projectId === projectId && grant.environmentId === environmentId));
    onChange(ordered(role === '' ? rest : [...rest, { projectId, environmentId, role: role as Role }], projects));
  };

  const rows: Row[] = projects.map((project) => ({
    key: `p${project.id}`,
    name: project.name,
    label: project.name,
    projectId: project.id,
    environmentId: null,
    children: project.environments.length
      ? project.environments.map((environment) => ({
          key: `e${environment.id}`,
          name: environment.name,
          label: `${project.name} / ${environment.name}`,
          projectId: project.id,
          environmentId: environment.id,
        }))
      : undefined,
  }));

  const columns: TableColumnsType<Row> = [
    {
      dataIndex: 'name',
      key: 'name',
      render: (name: string, row) => (row.environmentId === null ? <Text strong>{name}</Text> : <Text>{name}</Text>),
      title: 'Project / Environment',
    },
    {
      key: 'role',
      render: (_, row) => (
        <Select
          aria-label={`Role on ${row.label}`}
          disabled={readOnly}
          onChange={(role: string) => set(row.projectId, row.environmentId, role)}
          options={row.environmentId === null ? PROJECT_OPTIONS : ENVIRONMENT_OPTIONS}
          style={{ width: 180 }}
          value={roleOf(row.projectId, row.environmentId)}
        />
      ),
      title: 'Role',
      width: 200,
    },
  ];

  return (
    <Table<Row>
      columns={columns}
      dataSource={rows}
      expandable={{ defaultExpandAllRows: true }}
      pagination={false}
      rowKey="key"
      size="small"
    />
  );
}
