import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import { beginExternalProof, proveCurrentPassword, reauthenticationDestination } from "./ReauthenticationApi";

afterEach(() => vi.restoreAllMocks());

describe("fresh Session proof", () => {
  it("uses the dedicated current-Session endpoint and validates proof time", async () => {
    const session = { id: "current-session", client_type: "web", reauthenticated_at: 1_900_000_000_000 };
    const post = vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200, { data: session }));
    await expect(proveCurrentPassword("current-password")).resolves.toEqual({ kind: "success" });
    expect(post).toHaveBeenCalledWith("/api/v1/auth/reauthenticate/password", { body: { password: "current-password" } });
    for (const data of [{ ...session, reauthenticated_at: undefined }, { ...session, client_type: "desktop" }, { ...session, reauthenticated_at: 0 }]) {
      post.mockResolvedValue(apiResult(200, { data }));
      await expect(proveCurrentPassword("current-password")).resolves.toEqual({ kind: "unavailable" });
    }
  });

  it.each([
    ["authentication.invalid_credentials", "invalid_password"],
    ["authentication.reauthentication_method_required", "method_required"],
    ["authentication.rate_limited", "rate_limited"],
  ])("classifies %s", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(409, { problemCode }));
    await expect(proveCurrentPassword("current-password")).resolves.toEqual({ kind });
  });

  it("starts external proof with only the bounded task and rejects unsafe redirect schemes", async () => {
    const post = vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200, { data: { redirect_url: "https://sso.example/authorize?state=opaque" } }));
    await expect(beginExternalProof("connect-provider")).resolves.toEqual({ kind: "redirect", url: "https://sso.example/authorize?state=opaque" });
    expect(post).toHaveBeenCalledWith("/api/v1/auth/reauthenticate/external", { body: { task: "connect-provider" } });
    for (const redirect_url of ["javascript:alert(1)", "data:text/html,bad", "https://user:password@sso.example", "", "/relative"]) {
      post.mockResolvedValue(apiResult(200, { data: { redirect_url } }));
      await expect(beginExternalProof("security")).resolves.toEqual({ kind: "unavailable" });
    }
  });

  it("maps task destinations without retaining a submitted action", () => {
    expect(reauthenticationDestination("security")).toBe("/account/security");
    expect(reauthenticationDestination("connect-provider")).toBe("/account/connect-provider");
  });
});
