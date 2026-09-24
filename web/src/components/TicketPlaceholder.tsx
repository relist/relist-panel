import { EmptyState, PageHeader } from './primitives';

/**
 * A stand-in screen for a route whose real page lands with a later design-port
 * ticket. It exists only so every path resolves from the cutover on, built
 * from the ported primitives; the named ticket replaces the page that renders
 * it.
 */
export function TicketPlaceholder({
  description,
  ticket,
  title,
}: {
  description?: string;
  ticket: string;
  title: string;
}) {
  return (
    <>
      <PageHeader description={description} title={title} />
      <EmptyState description={`This screen is rebuilt by ${ticket} of the design port.`} title="Not ported yet" />
    </>
  );
}
