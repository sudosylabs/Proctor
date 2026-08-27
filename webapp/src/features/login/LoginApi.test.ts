import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import { authenticateLocal } from "./LoginApi";

const submission = {
  loginID: "person@example.edu",
  password: "password",
};

const session = {
  id: "session-id",
  client_type: "web",
};

describe("authenticateLocal", () => {
  afterEach(() => vi.restoreAllMocks());

  it("requires a web Session in the declared success response", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(200, { data: { session } }));
    await expect(authenticateLocal(submission)).resolves.toEqual({
      kind: "authenticated",
    });

    for (const data of [undefined, {}, { session: {} }, { session: { ...session, client_type: "desktop" } }]) {
      post.mockResolvedValue(apiResult(200, { data }));
      await expect(authenticateLocal(submission)).resolves.toEqual({
        kind: "unavailable",
      });
    }
  });

  it.each([
    ["authentication.mfa.required", "mfa_required"],
    ["authentication.mfa.invalid_code", "mfa_invalid"],
    ["authentication.invalid_credentials", "invalid_credentials"],
    ["authentication.mfa.unavailable", "mfa_unavailable"],
    ["authentication.sessions.maximum_reached", "session_limit"],
    ["authentication.rate_limited", "rate_limited"],
    ["unexpected.code", "unavailable"],
  ])("maps %s to %s", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(
      apiResult(401, { problemCode }),
    );
    await expect(authenticateLocal(submission)).resolves.toEqual({ kind });
  });

  it("fails safely for unexpected statuses and rejected requests", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(204));
    await expect(authenticateLocal(submission)).resolves.toEqual({
      kind: "unavailable",
    });

    post.mockRejectedValue(new Error("offline"));
    await expect(authenticateLocal(submission)).resolves.toEqual({
      kind: "unavailable",
    });
  });
});
