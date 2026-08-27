import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import { submitInstallation, type SetupSubmission } from "./SetupApi";

const submission: SetupSubmission = {
  bootstrapSecret: "secret",
  institutionName: "example-university",
  institutionDisplayName: "Example University",
  institutionDescription: "",
  administratorEmail: "admin@example.edu",
  administratorUsername: "admin",
  administratorDisplayName: "",
  password: "password",
};

describe("submitInstallation", () => {
  afterEach(() => vi.restoreAllMocks());

  it("requires the declared created response and a response object", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(201, { data: {} }));
    await expect(submitInstallation(submission)).resolves.toEqual({
      kind: "complete",
    });

    post.mockResolvedValue(apiResult(201));
    await expect(submitInstallation(submission)).resolves.toEqual({
      kind: "unavailable",
    });

    post.mockResolvedValue(apiResult(200, { data: {} }));
    await expect(submitInstallation(submission)).resolves.toEqual({
      kind: "unavailable",
    });
  });

  it.each([
    ["installation.already_initialized", "complete"],
    ["installation.bootstrap_denied", "bootstrap_denied"],
    ["authentication.password.invalid", "password_rejected"],
    ["authentication.rate_limited", "rate_limited"],
    ["unexpected.code", "unavailable"],
  ])("maps %s to %s", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(
      apiResult(400, { problemCode }),
    );
    await expect(submitInstallation(submission)).resolves.toEqual({ kind });
  });

  it("fails safely when the request rejects", async () => {
    vi.spyOn(apiClient, "POST").mockRejectedValue(new Error("offline"));
    await expect(submitInstallation(submission)).resolves.toEqual({
      kind: "unavailable",
    });
  });
});
