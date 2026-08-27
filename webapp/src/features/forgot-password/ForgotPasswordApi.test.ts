import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import { apiResult } from "../../test/ApiClientTestAdapter";
import { requestPasswordReset } from "./ForgotPasswordApi";

describe("requestPasswordReset", () => {
  afterEach(() => vi.restoreAllMocks());

  it("accepts only the declared accepted response", async () => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(202));
    await expect(requestPasswordReset(" person@example.edu ")).resolves.toEqual({
      kind: "accepted",
    });

    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200));
    await expect(requestPasswordReset("person@example.edu")).resolves.toEqual({
      kind: "unavailable",
    });
  });

  it("classifies rate limiting and conceals unknown problems", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(
      apiResult(429, { problemCode: "authentication.rate_limited" }),
    );
    await expect(requestPasswordReset("person@example.edu")).resolves.toEqual({
      kind: "rate_limited",
    });

    post.mockResolvedValue(apiResult(400, { problemCode: "request.invalid" }));
    await expect(requestPasswordReset("person@example.edu")).resolves.toEqual({
      kind: "unavailable",
    });
  });

  it("fails safely for malformed problems and rejected requests", async () => {
    const post = vi.spyOn(apiClient, "POST");
    post.mockResolvedValue(apiResult(429, { problemValue: { code: "authentication.rate_limited" } }));
    await expect(requestPasswordReset("person@example.edu")).resolves.toEqual({
      kind: "unavailable",
    });

    post.mockRejectedValue(new Error("offline"));
    await expect(requestPasswordReset("person@example.edu")).resolves.toEqual({
      kind: "unavailable",
    });
  });
});
