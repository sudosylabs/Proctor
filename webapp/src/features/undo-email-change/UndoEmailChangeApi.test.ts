import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import {
  resolveEmailChangeUndoResult,
  undoEmailChange,
} from "./UndoEmailChangeApi";

describe("resolveEmailChangeUndoResult", () => {
  it("accepts only the declared no-content success", () => {
    expect(resolveEmailChangeUndoResult({ status: 204 }, undefined)).toEqual({
      kind: "restored",
    });
    expect(resolveEmailChangeUndoResult({ status: 200 }, undefined)).toEqual({
      kind: "unavailable",
    });
  });

  it.each([
    ["authentication.account_token.invalid", "invalid"],
    ["request.invalid", "invalid"],
    ["authentication.email_change_undo.conflict", "conflict"],
    ["authentication.rate_limited", "rate_limited"],
    ["authentication.account_recovery.unavailable", "unavailable"],
    ["unexpected.code", "unavailable"],
  ])("maps %s to %s", (code, kind) => {
    expect(
      resolveEmailChangeUndoResult(
        { status: 400 },
        { type: "/problems/x", title: "Failed", status: 400, code },
      ),
    ).toEqual({ kind });
  });
});

describe("undoEmailChange", () => {
  afterEach(() => vi.restoreAllMocks());

  it("sends only the captured token", async () => {
    const post = vi
      .spyOn(apiClient, "POST")
      .mockResolvedValue(apiResult(204));
    await expect(undoEmailChange("token-value")).resolves.toEqual({
      kind: "restored",
    });
    expect(post).toHaveBeenCalledWith("/api/v1/auth/email-change/undo", {
      body: { token: "token-value" },
    });
  });

  it("fails safely when the request rejects", async () => {
    vi.spyOn(apiClient, "POST").mockRejectedValue(new Error("offline"));
    await expect(undoEmailChange("token-value")).resolves.toEqual({
      kind: "unavailable",
    });
  });
});
