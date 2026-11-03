import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import { completePasswordReset } from "./ResetPasswordApi";

describe("completePasswordReset", () => {
  afterEach(() => vi.restoreAllMocks());

  it("accepts only the declared no-content success", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(204));
    await expect(completePasswordReset("token", "password")).resolves.toEqual({
      kind: "complete",
    });

    post.mockResolvedValue(apiResult(200, { data: {} }));
    await expect(completePasswordReset("token", "password")).resolves.toEqual({
      kind: "unavailable",
    });
  });

  it.each([
    ["authentication.account_token.invalid", "invalid"],
    ["request.invalid", "invalid"],
    ["authentication.rate_limited", "rate_limited"],
    ["unexpected.code", "unavailable"],
  ])("maps %s to %s", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(
      apiResult(400, { problemCode }),
    );
    await expect(completePasswordReset("token", "password")).resolves.toEqual({
      kind,
    });
  });

  it.each([
    ["authentication.password.invalid", "length"],
    ["authentication.password.pattern_invalid", "pattern"],
    ["authentication.password.common_invalid", "common"],
    ["authentication.password.identity_invalid", "identity"],
    ["authentication.password.reused", "reused"],
  ])("maps %s to a %s password rejection", async (problemCode, reason) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(
      apiResult(400, { problemCode }),
    );
    await expect(completePasswordReset("token", "password")).resolves.toEqual({
      kind: "password_rejected",
      reason,
    });
  });

  it("fails safely when the request rejects", async () => {
    vi.spyOn(apiClient, "POST").mockRejectedValue(new Error("offline"));
    await expect(completePasswordReset("token", "password")).resolves.toEqual({
      kind: "unavailable",
    });
  });
});
