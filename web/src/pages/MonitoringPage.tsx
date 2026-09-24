import { TicketPlaceholder } from '../components/TicketPlaceholder';

export function MonitoringPage() {
  return (
    <TicketPlaceholder
      description="Host metrics over 15m / 1h / 6h / 24h windows, six charts to a page."
      ticket="the host-monitoring effort"
      title="Monitoring"
    />
  );
}
