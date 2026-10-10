import type { EffectiveVariable, ServiceBindings, SetUsage, VariableSet, VariableSource } from '../data/types';

/**
 * View decisions for Variable Sets and Service bindings, pure so row grouping,
 * stale detection and the Effective env marks are testable without a component.
 * Precedence is never derived here: the server orders and labels the layers.
 */

export type BindingRow = {
  service: string;
  setIds: number[];
};

export type BindingRows = {
  /** One row per Service in the saved YAML, in YAML order, bound or not. */
  live: BindingRow[];
  /** Bindings whose Service is no longer in the YAML; they can only be removed. */
  stale: BindingRow[];
};

/** Groups the API's bindings against the saved YAML's Services. */
export function bindingRows(services: readonly string[], bindings: readonly ServiceBindings[]): BindingRows {
  const bound = new Map(bindings.map((binding) => [binding.service, binding.setIds]));
  const declared = new Set(services);
  return {
    live: services.map((service) => ({ service, setIds: bound.get(service) ?? [] })),
    stale: bindings
      .filter((binding) => !declared.has(binding.service) && binding.setIds.length > 0)
      .map((binding) => ({ service: binding.service, setIds: binding.setIds })),
  };
}

/** The set's name for an id; an id the Environment no longer lists (a race with a delete) shows as `set #id`. */
export function setName(sets: readonly Pick<VariableSet, 'id' | 'name'>[], id: number): string {
  return sets.find((candidate) => candidate.id === id)?.name ?? `set #${id}`;
}

/** `compose/service`, the "used by" tag text. */
export function usageLabel(usage: Pick<SetUsage, 'composeName' | 'service'>): string {
  return `${usage.composeName}/${usage.service}`;
}

export function entryCountLabel(count: number): string {
  return `${count} ${count === 1 ? 'entry' : 'entries'}`;
}

export type SourceKind = 'environment' | 'compose' | 'set' | 'yaml';

export function sourceKind(source: VariableSource): SourceKind {
  return typeof source === 'string' ? source : 'set';
}

/** The tag text for a layer. A YAML key wins over everything, hence its own wording. */
export function sourceLabel(source: VariableSource): string {
  if (typeof source !== 'string') return source.set.name;
  switch (source) {
    case 'environment':
      return 'Environment';
    case 'compose':
      return 'Compose';
    case 'yaml':
      return 'set in YAML, wins';
  }
}

export type EffectiveRow = EffectiveVariable & {
  /** The merged row's key is also set by the YAML's `environment:`, which wins at runtime. */
  overriddenByYaml: boolean;
};

/** Marks merged rows whose key the saved YAML also sets; the YAML rows themselves stay as the server sent them. */
export function effectiveRows(variables: readonly EffectiveVariable[]): EffectiveRow[] {
  const yamlKeys = new Set(variables.filter((row) => row.source === 'yaml').map((row) => row.key));
  return variables.map((row) => ({ ...row, overriddenByYaml: row.source !== 'yaml' && yamlKeys.has(row.key) }));
}
