import type { Builder } from '../data/types';

/** N GB for whole gigabytes, else N MB. */
export function formatMemory(memoryMb: number): string {
  return memoryMb % 1024 === 0 ? `${memoryMb / 1024} GB` : `${memoryMb} MB`;
}

/** A Builder's name and limits, worded as the Deployment log names it. */
export function formatLimits(builder: Builder): string {
  const parts = [`${builder.cpus} CPU`, formatMemory(builder.memoryMb)];
  if (builder.maxParallelism) {
    parts.push(`${builder.maxParallelism} parallel steps`);
  }
  return `${builder.name} (${parts.join(', ')})`;
}

const STATES: Record<NonNullable<Builder['state']>, { color: string; label: string }> = {
  missing: { color: 'default', label: 'Not created yet' },
  running: { color: 'success', label: 'Running' },
  stopped: { color: 'default', label: 'Stopped' },
  unknown: { color: 'warning', label: 'Unknown' },
};

/** The status tag: a failed start wins over the container's state. */
export function builderStatus(builder: Builder): { color: string; label: string } {
  if (builder.lastError) {
    return { color: 'error', label: 'Error' };
  }
  return STATES[builder.state ?? 'unknown'];
}
