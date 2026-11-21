import type { BackupDestination, BackupDestinationSftp } from '../data/types';

export type DestinationTestState = 'failed' | 'passed' | 'untested';

/** What the last Test said: never run, passed or failed. */
export function destinationTestState(destination: BackupDestination): DestinationTestState {
  if (destination.lastTestAt === null) return 'untested';
  return destination.lastTestOk ? 'passed' : 'failed';
}

/** Bucket, then the prefix when there is one: "backups/relist". */
export function bucketLabel(destination: BackupDestination): string {
  if (destination.type !== 's3') return '';
  const { bucket, prefix } = destination.s3;
  return prefix ? `${bucket}/${prefix}` : bucket;
}

/** Where an SFTP destination is: `sftp://user@host:port/path`, with `:port` left out for 22. */
export function sftpLocation(sftp: BackupDestinationSftp): string {
  const host = sftp.host.includes(':') ? `[${sftp.host}]` : sftp.host;
  const port = sftp.port === 22 ? '' : `:${sftp.port}`;
  const path = sftp.path === '' || sftp.path.startsWith('/') ? sftp.path : `/${sftp.path}`;
  return `sftp://${sftp.user}@${host}${port}${path}`;
}

/** An endpoint the browser sends in the clear. */
export function isInsecureEndpoint(endpoint: string | undefined): boolean {
  return (endpoint ?? '').trim().toLowerCase().startsWith('http://');
}

/** The sentence a delete shows when copies stay in the destination. */
export function copiesWarning(copies: number): string {
  const noun = copies === 1 ? 'backup copy stays' : 'backup copies stay';
  return `${copies} ${noun} in the destination. The panel stops tracking them.`;
}

/** The numbers a 409 delete carries: copies left in the destination, or the schedules that use it. */
export function deleteConflict(body: unknown): { copies?: number; schedules?: string[] } {
  if (typeof body !== 'object' || body === null) return {};
  const { copies, schedules } = body as { copies?: unknown; schedules?: unknown };
  return {
    copies: typeof copies === 'number' ? copies : undefined,
    schedules: Array.isArray(schedules) ? (schedules as string[]) : undefined,
  };
}
