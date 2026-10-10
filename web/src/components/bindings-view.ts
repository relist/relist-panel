import type { EffectiveVariable, ServiceBindings, VariableSet, VariableSource } from '../data/types';

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

/** The set's name for an id; an id the Compose no longer lists (a race with a delete) shows as `set #id`. */
export function setName(sets: readonly Pick<VariableSet, 'id' | 'name'>[], id: number): string {
  return sets.find((candidate) => candidate.id === id)?.name ?? `set #${id}`;
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
  /** The server listed "yaml" among the layers: the YAML's `environment:` also sets this key and wins at runtime. */
  overriddenByYaml: boolean;
};

/**
 * Reads the server's "yaml" override into a flag and keeps it out of the
 * shadowed layers, which are real sources. The YAML's own rows stay as sent.
 */
export function effectiveRows(variables: readonly EffectiveVariable[]): EffectiveRow[] {
  return variables.map((row) => ({
    ...row,
    overriddenByYaml: row.overrides.includes('yaml'),
    overrides: row.overrides.filter((source) => source !== 'yaml'),
  }));
}

/** The delete confirmation's consequence line, naming the Services the set unbinds from. */
export function deleteSetNote(boundServices: readonly string[]): string {
  if (boundServices.length === 0) return 'Its entries are deleted with it.';
  return `Unbinds it from ${boundServices.join(', ')}. Takes effect on the next deploy.`;
}
