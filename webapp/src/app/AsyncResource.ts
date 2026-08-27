import { useCallback, useEffect, useRef, useState } from "react";

export interface AsyncResourceSnapshot<T> {
  hasResolved: boolean;
  loading: boolean;
  value: T;
}

export interface AsyncResourceController<T> {
  getSnapshot(): AsyncResourceSnapshot<T>;
  replace(value: T): void;
  retry(): Promise<T>;
  start(): Promise<T>;
  subscribe(listener: (snapshot: AsyncResourceSnapshot<T>) => void): () => void;
}

export function createAsyncResourceController<T>(
  load: () => Promise<T>,
  initialValue: T,
  initiallyLoading = true,
): AsyncResourceController<T> {
  let attempt = 0;
  let currentRequest:
    | { attempt: number; promise: Promise<T> }
    | undefined;
  let snapshot: AsyncResourceSnapshot<T> = {
    hasResolved: false,
    loading: initiallyLoading,
    value: initialValue,
  };
  const listeners = new Set<(next: AsyncResourceSnapshot<T>) => void>();

  function publish(next: AsyncResourceSnapshot<T>) {
    snapshot = next;
    for (const listener of listeners) {
      listener(snapshot);
    }
  }

  function begin(nextAttempt: number): Promise<T> {
    publish({ ...snapshot, loading: true });
    let promise: Promise<T>;
    try {
      promise = load();
    } catch (error) {
      promise = Promise.reject(error);
    }
    currentRequest = { attempt: nextAttempt, promise };
    void promise.then(
      (value) => {
        if (currentRequest?.attempt === nextAttempt) {
          publish({ hasResolved: true, loading: false, value });
        }
      },
      () => {
        if (currentRequest?.attempt === nextAttempt) {
          publish({ ...snapshot, loading: false });
        }
      },
    );
    return promise;
  }

  return {
    getSnapshot() {
      return snapshot;
    },
    replace(value) {
      attempt += 1;
      currentRequest = undefined;
      publish({ hasResolved: true, loading: false, value });
    },
    retry() {
      attempt += 1;
      return begin(attempt);
    },
    start() {
      if (currentRequest?.attempt === attempt) {
        return currentRequest.promise;
      }
      return begin(attempt);
    },
    subscribe(listener) {
      listeners.add(listener);
      listener(snapshot);
      return () => listeners.delete(listener);
    },
  };
}

export interface AsyncResource<T> extends AsyncResourceSnapshot<T> {
  replace(value: T): void;
  retry(): void;
}

export function useAsyncResource<T>(
  load: () => Promise<T>,
  initialValue: T,
  enabled = true,
): AsyncResource<T> {
  const loadRef = useRef(load);
  loadRef.current = load;
  const controllerRef = useRef<AsyncResourceController<T>>(undefined);
  controllerRef.current ??= createAsyncResourceController(
    () => loadRef.current(),
    initialValue,
    enabled,
  );
  const controller = controllerRef.current;
  const [snapshot, setSnapshot] = useState(controller.getSnapshot);

  useEffect(() => {
    const unsubscribe = controller.subscribe(setSnapshot);
    if (enabled) {
      void controller.start();
    }
    return unsubscribe;
  }, [controller, enabled]);

  const replace = useCallback(
    (value: T) => controller.replace(value),
    [controller],
  );
  const retry = useCallback(() => {
    void controller.retry();
  }, [controller]);

  return { ...snapshot, replace, retry };
}
