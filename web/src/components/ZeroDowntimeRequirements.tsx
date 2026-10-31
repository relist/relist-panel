/**
 * What a Service needs before zero-downtime deploy can roll it out (ADR-0023).
 * The confirm modal and the note under the YAML editor both render this one
 * list, so the two can't drift apart.
 */
export const ZERO_DOWNTIME_REQUIREMENTS: readonly string[] = [
  'A healthcheck that passes only once the app can serve requests',
  'No container_name',
  'No fixed host port in ports: ("80" is fine, "8080:80" is not)',
  'One replica (no deploy.replicas or scale above 1)',
  'No anonymous volumes: mount a named volume over every path the app writes to, including paths the image declares with VOLUME',
  'The app finishes in-flight requests when it gets SIGTERM, within stop_grace_period',
  'The app tolerates two copies running for a few seconds (no SQLite file shared through a named volume, no migrations that lock out the old version)',
];

export function ZeroDowntimeRequirements() {
  return (
    <ul style={{ margin: 0, paddingInlineStart: 20 }}>
      {ZERO_DOWNTIME_REQUIREMENTS.map((line) => (
        <li key={line}>{line}</li>
      ))}
    </ul>
  );
}
