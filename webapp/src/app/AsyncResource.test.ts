import { describe, expect, it, vi } from "vitest";

import { createAsyncResourceController } from "./AsyncResource";

interface Deferred<T> {
  promise: Promise<T>;
  resolve(value: T): void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
}

async function settle() {
  await Promise.resolve();
  await Promise.resolve();
}

describe("createAsyncResourceController", () => {
  it("reuses one initial promise across StrictMode-style subscriptions", async () => {
    const pending = deferred<string>();
    const load = vi.fn(() => pending.promise);
    const resource = createAsyncResourceController(load, "initial");
    const firstSubscription = resource.subscribe(() => undefined);
    const first = resource.start();
    firstSubscription();
    const secondSubscription = resource.subscribe(() => undefined);
    const second = resource.start();

    expect(first).toBe(second);
    expect(load).toHaveBeenCalledTimes(1);
    pending.resolve("ready");
    await second;
    await settle();
    expect(resource.getSnapshot()).toEqual({
      hasResolved: true,
      loading: false,
      value: "ready",
    });
    secondSubscription();
  });

  it("does not notify a subscriber after unmount", async () => {
    const pending = deferred<string>();
    const resource = createAsyncResourceController(() => pending.promise, "initial");
    const listener = vi.fn();
    const unsubscribe = resource.subscribe(listener);
    void resource.start();
    unsubscribe();
    const callsBeforeResolution = listener.mock.calls.length;

    pending.resolve("ready");
    await settle();
    expect(listener).toHaveBeenCalledTimes(callsBeforeResolution);
  });

  it("marks every retry as loading and applies only the newest result", async () => {
    const first = deferred<string>();
    const second = deferred<string>();
    const third = deferred<string>();
    const load = vi
      .fn<() => Promise<string>>()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
      .mockReturnValueOnce(third.promise);
    const resource = createAsyncResourceController(load, "initial");
    const snapshots: string[] = [];
    resource.subscribe((snapshot) => {
      snapshots.push(`${snapshot.loading}:${snapshot.value}`);
    });

    void resource.start();
    void resource.retry();
    void resource.retry();
    expect(load).toHaveBeenCalledTimes(3);
    expect(snapshots.filter((value) => value.startsWith("true:"))).toHaveLength(4);

    third.resolve("third");
    await settle();
    second.resolve("second");
    first.resolve("first");
    await settle();
    expect(resource.getSnapshot()).toEqual({
      hasResolved: true,
      loading: false,
      value: "third",
    });
  });

  it("supports retry after a failed attempt", async () => {
    const load = vi
      .fn<() => Promise<string>>()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce("ready");
    const resource = createAsyncResourceController(load, "initial");

    await expect(resource.start()).rejects.toThrow("offline");
    await settle();
    expect(resource.getSnapshot().loading).toBe(false);
    await expect(resource.retry()).resolves.toBe("ready");
    await settle();
    expect(resource.getSnapshot()).toEqual({
      hasResolved: true,
      loading: false,
      value: "ready",
    });
  });

  it("invalidates pending work when a feature replaces the value", async () => {
    const pending = deferred<string>();
    const resource = createAsyncResourceController(() => pending.promise, "initial");
    void resource.start();
    resource.replace("terminal");
    pending.resolve("stale");
    await settle();
    expect(resource.getSnapshot().value).toBe("terminal");
  });
});
