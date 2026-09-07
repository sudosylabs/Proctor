import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import {
  activateAuthenticator, beginAuthenticatorSetup, challengeSession, disableAuthenticator,
  regenerateRecoveryCodes, requestSecurityContext,
} from "./AccountSecurityApi";

const context = {
  enabled: false, pending: false, recovery_codes_remaining: 0,
  service_enabled: true, mfa_recovery_required: false,
  authentication_method: "password", authentication_strength: "single_factor", recently_authenticated: true,
};

afterEach(() => vi.restoreAllMocks());

describe("bounded account security context", () => {
  it("reads recovery context even when the institution disables the service", async () => {
    const get = vi.spyOn(apiClient, "GET").mockResolvedValue(apiResult(200, {
      data: { ...context, service_enabled: false, mfa_recovery_required: true },
    }));
    await expect(requestSecurityContext()).resolves.toMatchObject({
      kind: "ready", context: { serviceEnabled: false, recoveryRequired: true, authenticationMethod: "password" },
    });
    expect(get).toHaveBeenCalledWith("/api/v1/users/me/mfa");
  });

  it("fails closed for incomplete and malformed status projections", async () => {
    const get = vi.spyOn(apiClient, "GET");
    for (const data of [undefined, {}, { ...context, service_enabled: undefined },
      { ...context, mfa_recovery_required: "false" }, { ...context, recovery_codes_remaining: -1 },
      { ...context, pending_expires_at: Infinity }, { ...context, authentication_method: "" }]) {
      get.mockResolvedValue(apiResult(200, { data }));
      await expect(requestSecurityContext()).resolves.toEqual({ kind: "unavailable" });
    }
    get.mockResolvedValue(apiResult(401, { problemCode: "authentication.required" }));
    await expect(requestSecurityContext()).resolves.toEqual({ kind: "no_session" });
  });
});

describe("explicit MFA mutations", () => {
  it("projects only the setup key and expiry from a valid creation response", async () => {
    const post = vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(201, {
      data: { secret: "SETUPKEY", provisioning_uri: "otpauth://secret-not-projected", expires_at: 1_900_000_000_000 },
    }));
    await expect(beginAuthenticatorSetup()).resolves.toEqual({ kind: "success", setup: { secret: "SETUPKEY", expiresAt: 1_900_000_000_000 } });
    expect(post).toHaveBeenCalledOnce();
    post.mockResolvedValue(apiResult(201, { data: { secret: "SETUPKEY", expires_at: 1e20 } }));
    await expect(beginAuthenticatorSetup()).resolves.toEqual({ kind: "unavailable" });
  });

  it("requires a bounded unique code set in an exact activation or regeneration success", async () => {
    const post = vi.spyOn(apiClient, "POST");
    for (const action of [() => activateAuthenticator("123456"), regenerateRecoveryCodes]) {
      post.mockResolvedValue(apiResult(200, { data: { recovery_codes: ["code-one", "code-two"] } }));
      await expect(action()).resolves.toEqual({ kind: "success", codes: ["code-one", "code-two"] });
      for (const recovery_codes of [[], [""], [123], ["same", "same"], Array.from({ length: 101 }, (_, i) => `code-${i}`)]) {
        post.mockResolvedValue(apiResult(200, { data: { recovery_codes } }));
        await expect(action()).resolves.toEqual({ kind: "unavailable" });
      }
      post.mockResolvedValue(apiResult(204));
      await expect(action()).resolves.toEqual({ kind: "unavailable" });
      post.mockRejectedValue(new Error("response was lost"));
      await expect(action()).resolves.toEqual({ kind: "unavailable" });
    }
  });

  it("passes recovery codes unchanged and requires a Web Session for challenge success", async () => {
    const post = vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200, { data: { id: "session", client_type: "web" } }));
    await expect(challengeSession("ABCD-EFGH")).resolves.toEqual({ kind: "success" });
    expect(post).toHaveBeenCalledWith("/api/v1/users/me/mfa/challenge", { body: { code: "ABCD-EFGH" } });
    post.mockResolvedValue(apiResult(200, { data: { id: "session", client_type: "desktop" } }));
    await expect(challengeSession("ABCD-EFGH")).resolves.toEqual({ kind: "unavailable" });
  });

  it.each([
    ["authentication.reauthentication_required", "primary_required"],
    ["authentication.strong_required", "strong_required"],
    ["authentication.mfa.invalid_code", "invalid_code"],
    ["authentication.mfa.disabled", "disabled"],
    ["authentication.mfa.conflict", "conflict"],
    ["authentication.mfa.not_found", "conflict"],
    ["authentication.rate_limited", "rate_limited"],
    ["untrusted.server.detail", "unavailable"],
  ])("maps %s without forwarding server text", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(403, { problemCode }));
    await expect(activateAuthenticator("123456")).resolves.toEqual({ kind });
  });

  it("accepts only the declared no-content disable success", async () => {
    const post = vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(204));
    await expect(disableAuthenticator()).resolves.toEqual({ kind: "success" });
    post.mockResolvedValue(apiResult(200));
    await expect(disableAuthenticator()).resolves.toEqual({ kind: "unavailable" });
  });
});
