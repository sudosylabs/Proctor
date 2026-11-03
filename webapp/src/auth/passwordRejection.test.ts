import { describe, expect, it } from "vitest";

import {
  passwordRejection,
  passwordRejectionMessage,
} from "./passwordRejection";

describe("passwordRejection", () => {
  it.each([
    ["authentication.password.invalid", "length"],
    ["authentication.password.pattern_invalid", "pattern"],
    ["authentication.password.common_invalid", "common"],
    ["authentication.password.identity_invalid", "identity"],
    ["authentication.password.reused", "reused"],
  ])("names %s as a %s rejection", (code, rejection) => {
    expect(passwordRejection(code)).toBe(rejection);
  });

  it.each([undefined, "authentication.rate_limited", "toString", "__proto__"])(
    "ignores %s",
    (code) => {
      expect(passwordRejection(code)).toBeUndefined();
    },
  );
});

describe("passwordRejectionMessage", () => {
  it("keeps the page's length wording and shares the policy explanations", () => {
    expect(
      passwordRejectionMessage("length", "webapp.join.form.error.password_invalid"),
    ).toBe("webapp.join.form.error.password_invalid");
    expect(
      passwordRejectionMessage("pattern", "webapp.join.form.error.password_invalid"),
    ).toBe("webapp.password_policy.pattern");
    expect(
      passwordRejectionMessage("common", "webapp.join.form.error.password_invalid"),
    ).toBe("webapp.password_policy.common");
    expect(
      passwordRejectionMessage("identity", "webapp.join.form.error.password_invalid"),
    ).toBe("webapp.password_policy.identity");
    expect(
      passwordRejectionMessage("reused", "webapp.join.form.error.password_invalid"),
    ).toBe("webapp.password_policy.reused");
  });
});
