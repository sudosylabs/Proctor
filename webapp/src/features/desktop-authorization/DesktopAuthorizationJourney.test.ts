import { describe, expect, it, vi } from "vitest";

import type { DesktopAuthorizationContext } from "./DesktopAuthorizationApi";
import { createDesktopAuthorizationJourney, desktopJourneyAPI } from "./DesktopAuthorizationJourney";

const bound: DesktopAuthorizationContext = {
  state: "bound", installation: "Example University", deviceName: "Exam laptop", expiresAt: 1000,
  localLoginEnabled: true, externalProviders: [{ id: "campus", display_name: "Campus", type: "oidc" }],
};
const authenticated: DesktopAuthorizationContext = {
  ...bound, state: "authenticated", account: { id: "user-1", username: "student", display_name: "Student" },
};
const input = { servingOrigin: "https://proctor.example", state: "state", proof: { state: "state", handle: "handle", browserProof: "proof" } };

function setup(currentSession = true) {
  const api = {
    approve: vi.fn<typeof desktopJourneyAPI.approve>().mockResolvedValue({ kind: "approved", redirectURL: "http://127.0.0.1:55000/callback" }),
    authenticate: vi.fn<typeof desktopJourneyAPI.authenticate>().mockResolvedValue({ kind: "authenticated", context: authenticated }),
    authenticateSession: vi.fn<typeof desktopJourneyAPI.authenticateSession>().mockResolvedValue(currentSession
      ? { kind: "authenticated", context: authenticated } : { kind: "no_session" }),
    bind: vi.fn<typeof desktopJourneyAPI.bind>().mockResolvedValue({ kind: "bound" }),
    cancel: vi.fn<typeof desktopJourneyAPI.cancel>().mockResolvedValue({ kind: "cancelled" }),
    context: vi.fn<typeof desktopJourneyAPI.context>().mockResolvedValue({ kind: "ready", context: bound }),
    providerURL: vi.fn<typeof desktopJourneyAPI.providerURL>().mockReturnValue("/api/v1/auth/desktop/authorizations/authenticate/providers/campus/login?state=state"),
    reset: vi.fn<typeof desktopJourneyAPI.reset>().mockResolvedValue({ kind: "reset" }),
  };
  const navigate = vi.fn();
  const journey = createDesktopAuthorizationJourney(input, api, navigate);
  return { journey, api, navigate };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("Desktop Authorization journey", () => {
  it("keeps a missing request terminal without loading or making requests", async () => {
    const { api } = setup();
    const journey = createDesktopAuthorizationJourney({ servingOrigin: input.servingOrigin }, api);
    expect(journey.getSnapshot().view.kind).toBe("invalid");
    await journey.start();
    expect(journey.getSnapshot().view.kind).toBe("invalid");
    expect(api.bind).not.toHaveBeenCalled();
    expect(api.context).not.toHaveBeenCalled();
  });
  it("reuses initial binding across a Strict Mode effect restart", async () => {
    const { journey, api } = setup();
    const binding = deferred<{ kind: "bound" }>();
    api.bind.mockReturnValue(binding.promise);
    const first = journey.start();
    journey.stop();
    const restarted = journey.start();
    expect(restarted).toBe(first);
    binding.resolve({ kind: "bound" });
    await restarted;
    expect(api.bind).toHaveBeenCalledTimes(1);
    expect(journey.getSnapshot().view.kind).toBe("confirmation");
  });

  it("discards confirmation after reset and retries without reapplying the Web Session", async () => {
    const { journey, api } = setup();
    await journey.start();
    api.context.mockResolvedValueOnce({ kind: "unavailable" });
    await journey.useAnotherAccount();
    expect(journey.getSnapshot().view).toEqual({ kind: "unavailable" });
    expect(JSON.stringify(journey.getSnapshot())).not.toContain("student");
    await journey.approve();
    expect(api.approve).not.toHaveBeenCalled();
    journey.retry();
    await vi.waitFor(() => expect(journey.getSnapshot().view.kind).toBe("authentication"));
    expect(api.authenticateSession).toHaveBeenCalledTimes(1);
    expect(api.reset).toHaveBeenCalledTimes(1);
    expect(api.bind).toHaveBeenCalledTimes(1);
  });

  it("also discards confirmation when the reset result is ambiguous", async () => {
    const { journey, api } = setup();
    await journey.start();
    api.reset.mockRejectedValue(new Error("connection lost"));
    await journey.useAnotherAccount();
    expect(journey.getSnapshot().view.kind).toBe("unavailable");
    expect(journey.getSnapshot().pending).toBeUndefined();
    journey.retry();
    await vi.waitFor(() => expect(journey.getSnapshot().view.kind).toBe("authentication"));
    expect(api.authenticateSession).toHaveBeenCalledTimes(1);
  });

  it("serializes local authentication, cancellation, provider navigation and repeated submission", async () => {
    const { journey, api, navigate } = setup(false);
    await journey.start();
    const authentication = deferred<{ kind: "authenticated"; context: DesktopAuthorizationContext }>();
    api.authenticate.mockReturnValue(authentication.promise);
    const first = journey.authenticate({ loginID: "student", password: "private" });
    await journey.authenticate({ loginID: "student", password: "private" });
    await journey.cancel();
    journey.chooseProvider("campus");
    journey.retry();
    expect(api.authenticate).toHaveBeenCalledTimes(1);
    expect(api.cancel).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
    expect(journey.getSnapshot().pending).toBe("authenticate");
    authentication.resolve({ kind: "authenticated", context: authenticated });
    expect(await first).toBe("authenticated");
    expect(journey.getSnapshot().view.kind).toBe("confirmation");
    expect(JSON.stringify(journey.getSnapshot())).not.toContain("private");
  });

  it("ignores an approval completion after unmount", async () => {
    const { journey, api, navigate } = setup();
    await journey.start();
    const approval = deferred<{ kind: "approved"; redirectURL: string }>();
    api.approve.mockReturnValue(approval.promise);
    const first = journey.approve();
    await journey.approve();
    await journey.useAnotherAccount();
    expect(api.approve).toHaveBeenCalledTimes(1);
    expect(api.reset).not.toHaveBeenCalled();
    journey.stop();
    approval.resolve({ kind: "approved", redirectURL: "http://127.0.0.1:55000/callback" });
    await first;
    expect(navigate).not.toHaveBeenCalled();
    expect(journey.getSnapshot().view.kind).not.toBe("approved");
  });

  it("keeps bounded form outcomes local and recovers from thrown adapters", async () => {
    const { journey, api } = setup(false);
    await journey.start();
    api.authenticate.mockResolvedValueOnce({ kind: "mfa_required" });
    expect(await journey.authenticate({ loginID: "student", password: "private" })).toBe("mfa_required");
    expect(journey.getSnapshot().view.kind).toBe("authentication");
    api.authenticate.mockRejectedValueOnce(new Error("offline"));
    await journey.authenticate({ loginID: "student", password: "private" });
    expect(journey.getSnapshot().feedback).toBe("unavailable");
    expect(journey.getSnapshot().pending).toBeUndefined();
    await journey.cancel();
    expect(journey.getSnapshot().view.kind).toBe("cancelled");
    expect(journey.getSnapshot().focusHeading).toBe(true);
  });

  it("only navigates to a provider in the current context", async () => {
    const { journey, api, navigate } = setup(false);
    await journey.start();
    journey.chooseProvider("unlisted");
    expect(navigate).not.toHaveBeenCalled();
    journey.chooseProvider("campus");
    journey.chooseProvider("campus");
    expect(api.providerURL).toHaveBeenCalledExactlyOnceWith("campus", "state");
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(journey.getSnapshot().pending).toBe("provider");
  });
});
