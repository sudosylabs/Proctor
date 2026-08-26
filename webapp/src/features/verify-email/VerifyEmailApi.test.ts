import { describe, expect, it } from "vitest";

import { resolveEmailVerificationResult } from "./VerifyEmailApi";

describe("resolveEmailVerificationResult", () => {
  it("accepts only the declared no-content success", () => {
    expect(resolveEmailVerificationResult({ status: 204 }, undefined)).toEqual({
      kind: "verified",
    });
    expect(resolveEmailVerificationResult({ status: 200 }, undefined)).toEqual({
      kind: "unavailable",
    });
  });

  it("maps every concealed terminal token outcome to one invalid state", () => {
    for (const code of [
      "authentication.account_token.invalid",
      "request.invalid",
    ]) {
      expect(
        resolveEmailVerificationResult(
          { status: 400 },
          {
            type: "/problems/email-verification",
            title: "Verification failed",
            status: 400,
            code,
            private_value: "must not escape",
          },
        ),
      ).toEqual({ kind: "invalid" });
    }
  });

  it("keeps transport, rate-limit, and server failures retryable", () => {
    expect(
      resolveEmailVerificationResult(
        { status: 503 },
        {
          type: "/problems/unavailable",
          title: "Unavailable",
          status: 503,
          code: "authentication.account_recovery.unavailable",
        },
      ),
    ).toEqual({ kind: "unavailable" });
  });
});
