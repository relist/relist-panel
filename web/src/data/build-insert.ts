import { isMap, parseDocument } from 'yaml';

import type { BuildTarget } from './types';

/**
 * Adds a `build:` block per target to a Compose YAML draft, keeping comments.
 * A new Service also gets `restart: unless-stopped`; an existing Service keeps
 * its `image:` unless the target asks to replace it.
 */
export function insertBuildBlocks(draft: string, targets: BuildTarget[]): string {
  const doc = parseDocument(draft);
  if (!isMap(doc.get('services'))) {
    doc.set('services', doc.createNode({}));
  }
  for (const target of targets) {
    const base = ['services', target.service];
    const build: Record<string, string> = { context: target.context, dockerfile: target.dockerfile };
    if (target.target) {
      build.target = target.target;
    }
    const exists = doc.hasIn(base);
    doc.setIn([...base, 'build'], doc.createNode(build));
    if (!exists) {
      doc.setIn([...base, 'restart'], 'unless-stopped');
    } else if (target.replaceImage) {
      doc.deleteIn([...base, 'image']);
    }
  }
  return doc.toString();
}

