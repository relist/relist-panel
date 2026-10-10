import { Typography } from 'antd';

import { usePanel } from '../data/store';
import type { Compose, ComposeVariable, Environment } from '../data/types';
import { ComposeBindingsCard } from './ComposeBindingsCard';
import { RedeployBanner } from './RedeployBanner';
import { VariablesEditor } from './VariablesPanel';
import { overrideHint } from './variables-view';

const { Text } = Typography;

/**
 * The Compose screen's Variables tab: this Compose's own variables, which
 * override an Environment Variable of the same key for this Compose only. The
 * list is the page's (it also feeds the Editor tab's Environment card); each
 * write refreshes the Compose, so the banner follows `variablesChangedAt`.
 */
export function ComposeVariablesTab({
  compose,
  deploying,
  environment,
  loadError,
  onChanged,
  onDeploy,
  onError,
  variables,
}: {
  compose: Compose;
  deploying: boolean;
  environment: Environment;
  /** Why the list could not be loaded, shown instead of the empty state. */
  loadError?: string;
  /** Called after a write, to refetch the list. */
  onChanged: () => void;
  onDeploy: () => void;
  onError: (reason: unknown) => void;
  /** `undefined` while the first load is in flight. */
  variables?: ComposeVariable[];
}) {
  const { deleteComposeVariable, isPending, saveComposeVariable } = usePanel();

  return (
    <>
      <RedeployBanner compose={compose} deploying={deploying} onDeploy={onDeploy} />
      <VariablesEditor
        annotate={(variable) => {
          const hint = overrideHint(variable.key, environment.name, environment.variables);
          return hint ? (
            <Text style={{ display: 'block', fontSize: 12 }} type="secondary">
              {hint}
            </Text>
          ) : null;
        }}
        deleteNote="The Environment Variable of the same key, if any, applies again on the next deploy."
        description={
          <>
            Only for this Compose, usable as <Text code>{'${VAR}'}</Text>. A key that is also an Environment Variable
            overrides it here. Changes take effect on the next deploy.
          </>
        }
        emptyDescription={
          loadError
            ? `Could not load this Compose's variables: ${loadError}`
            : 'Values only this Compose needs, or that replace an Environment Variable for it.'
        }
        emptyTitle={loadError ? 'Variables unavailable' : 'No Compose variables yet'}
        isDeleting={(variable) => isPending(`delete-compose-variable:${variable.id}`)}
        loading={variables === undefined && !loadError}
        noun="Compose variables"
        onDelete={async (variable) => {
          await deleteComposeVariable(compose.id, variable.id);
          onChanged();
        }}
        onError={onError}
        onSave={async (values, id) => {
          await saveComposeVariable(compose.id, values, id);
          onChanged();
        }}
        singleAddWhenEmpty
        saving={isPending(`save-compose-variable:${compose.id}`)}
        title="Compose variables"
        variables={variables ?? []}
      />
      <ComposeBindingsCard compose={compose} environment={environment} onError={onError} />
    </>
  );
}
