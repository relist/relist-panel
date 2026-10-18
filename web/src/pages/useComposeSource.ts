import { useCallback, useLayoutEffect, useRef, useState } from 'react';

import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import { sourceLoadState } from '../data/source-view';
import type { ComposeSource } from '../data/types';

type SourceRead = {
  path: ComposePath;
  loading: boolean;
  value?: ComposeSource | null;
  error?: string;
};

function samePath(left: ComposePath | undefined, right: ComposePath | undefined): boolean {
  return left !== undefined && right !== undefined
    && left.composeId === right.composeId
    && left.environmentId === right.environmentId
    && left.projectId === right.projectId;
}

/**
 * Reads the Source on load and after its card or modal changes it. Only the
 * latest request in the current context may change the read or report errors.
 * A refresh retains the known Source for the card/modal, while Deploy waits.
 */
export function useComposeSource(path: ComposePath | undefined, onError: (message: string) => void) {
  const [read, setRead] = useState<SourceRead>();
  const request = useRef(0);
  const mounted = useRef(false);
  const pathRef = useRef(path);
  pathRef.current = path;
  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;
  const projectId = path?.projectId;
  const environmentId = path?.environmentId;
  const composeId = path?.composeId;
  const refresh = useCallback(() => {
    if (!mounted.current || composeId === undefined || environmentId === undefined || projectId === undefined) return;
    const location = { composeId, environmentId, projectId };
    // A modal callback retained from the previous context cannot cancel or
    // start a request for the page the operator has since navigated to.
    if (!samePath(location, pathRef.current)) return;
    const mine = ++request.current;
    setRead((previous) => ({
      path: location,
      loading: true,
      value: samePath(previous?.path, location) ? previous?.value : undefined,
    }));
    api.getSource(location)
      .then((value) => {
        if (request.current !== mine || !samePath(location, pathRef.current)) return;
        setRead({ path: location, loading: false, value });
      })
      .catch((reason: unknown) => {
        if (request.current !== mine || !samePath(location, pathRef.current)) return;
        const text = reason instanceof Error ? reason.message : 'Something went wrong';
        setRead((previous) => ({
          path: location,
          loading: false,
          error: text,
          value: samePath(previous?.path, location) ? previous?.value : undefined,
        }));
        onErrorRef.current(text);
      });
  }, [composeId, environmentId, projectId]);

  // Discard the previous context before paint, including when a temporarily
  // missing Project/Environment returns with the same IDs. Cleanup invalidates
  // every pending callback on navigation and unmount.
  useLayoutEffect(() => {
    mounted.current = true;
    setRead(undefined);
    refresh();
    return () => {
      mounted.current = false;
      request.current += 1;
    };
  }, [refresh]);

  const current = samePath(read?.path, path) ? read : undefined;
  const loaded = current && !current.loading && current.value !== undefined
    ? { composeId: current.path.composeId, value: current.value }
    : undefined;

  return {
    error: current?.error,
    refresh,
    source: current?.value ?? null,
    state: sourceLoadState(loaded, current?.error !== undefined ? composeId : undefined, composeId ?? NaN),
  };
}
