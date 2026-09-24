import { useParams } from 'react-router';

import { TicketPlaceholder } from '../components/TicketPlaceholder';

export function ProjectPage() {
  const { environmentId, projectId, tab } = useParams();
  return (
    <TicketPlaceholder
      description={
        environmentId
          ? `Environment ${environmentId}${tab ? ` and the “${tab}” tab` : ''} arrive in the URL — the Environment switcher and the Composes | Variables | Settings tabs render here.`
          : 'The Environment switcher and the Composes | Variables | Settings tabs render here.'
      }
      ticket="06 — Project"
      title={`Project ${projectId}`}
    />
  );
}
