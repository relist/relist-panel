import { useParams } from 'react-router';

import { TicketPlaceholder } from '../components/TicketPlaceholder';

export function ComposePage() {
  const { composeId, tab } = useParams();
  return (
    <TicketPlaceholder
      description={tab ? `The “${tab}” tab arrives in the URL, so it is linkable.` : undefined}
      ticket="07 — Compose"
      title={`Compose ${composeId}`}
    />
  );
}
