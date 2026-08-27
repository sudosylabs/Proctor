import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import {
  acceptInvitationAccount,
  acceptInvitationSession,
  type InvitationAccountSubmission,
} from "./InvitationApi";

const acceptance = {
  invitation_id: "invitation-id",
  user_id: "user-id",
  replayed: false,
};

const submission: InvitationAccountSubmission = {
  firstName: "Ada",
  handle: "transaction-handle",
  lastName: "Lovelace",
  password: "password",
  username: "ada",
};

describe("acceptInvitationAccount", () => {
  afterEach(() => vi.restoreAllMocks());

  it("requires the declared response and validates its body", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(200, { data: acceptance }));
    await expect(acceptInvitationAccount(submission)).resolves.toEqual({
      kind: "accepted",
    });

    for (const data of [undefined, {}, { ...acceptance, replayed: "no" }]) {
      post.mockResolvedValue(apiResult(200, { data }));
      await expect(acceptInvitationAccount(submission)).resolves.toEqual({
        kind: "unavailable",
      });
    }
  });

  it.each([
    ["authentication.password.invalid", "password_rejected"],
    ["invitation.invalid", "details_invalid"],
    ["invitation.user_invalid", "details_invalid"],
    ["authentication.rate_limited", "rate_limited"],
    ["unexpected.code", "unavailable"],
  ])("maps %s to %s", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(
      apiResult(400, { problemCode }),
    );
    await expect(acceptInvitationAccount(submission)).resolves.toEqual({ kind });
  });

  it("fails safely when the request rejects", async () => {
    vi.spyOn(apiClient, "POST").mockRejectedValue(new Error("offline"));
    await expect(acceptInvitationAccount(submission)).resolves.toEqual({
      kind: "unavailable",
    });
  });
});

describe("acceptInvitationSession", () => {
  afterEach(() => vi.restoreAllMocks());

  it("validates success bodies", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(200, { data: acceptance }));
    await expect(acceptInvitationSession("handle")).resolves.toEqual({
      kind: "accepted",
    });

    post.mockResolvedValue(apiResult(200, { data: {} }));
    await expect(acceptInvitationSession("handle")).resolves.toEqual({
      kind: "unavailable",
    });
  });

  it.each([
    ["authentication.required", "session_required"],
    ["authentication.invalid_token", "session_required"],
    ["unexpected.code", "unavailable"],
  ])("maps %s to %s", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(
      apiResult(401, { problemCode }),
    );
    await expect(acceptInvitationSession("handle")).resolves.toEqual({ kind });
  });

  it("fails safely when the request rejects", async () => {
    vi.spyOn(apiClient, "POST").mockRejectedValue(new Error("offline"));
    await expect(acceptInvitationSession("handle")).resolves.toEqual({
      kind: "unavailable",
    });
  });
});
