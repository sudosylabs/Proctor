import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import {
  submitRegistration,
  type RegistrationSubmission,
} from "./RegistrationApi";

const submission: RegistrationSubmission = {
  email: "person@example.edu",
  firstName: "Ada",
  lastName: "Lovelace",
  username: "ada",
  password: "password",
};

describe("submitRegistration", () => {
  afterEach(() => vi.restoreAllMocks());

  it("accepts only the declared accepted response", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(202));
    await expect(submitRegistration(submission)).resolves.toEqual({
      kind: "accepted",
    });

    post.mockResolvedValue(apiResult(200, { data: {} }));
    await expect(submitRegistration(submission)).resolves.toEqual({
      kind: "unavailable",
    });
  });

  it.each([
    ["authentication.password.invalid", "password_rejected"],
    ["authentication.registration.invalid", "details_invalid"],
    ["authentication.registration.invitation_required", "invitation_required"],
    ["authentication.registration.unavailable", "admission_unavailable"],
    ["authentication.rate_limited", "rate_limited"],
    ["unexpected.code", "unavailable"],
  ])("maps %s to %s", async (problemCode, kind) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(
      apiResult(400, { problemCode }),
    );
    await expect(submitRegistration(submission)).resolves.toEqual({ kind });
  });

  it("fails safely when the request rejects", async () => {
    vi.spyOn(apiClient, "POST").mockRejectedValue(new Error("offline"));
    await expect(submitRegistration(submission)).resolves.toEqual({
      kind: "unavailable",
    });
  });
});
