import { describe, expect, it } from "vitest";

import { isCurrentUser } from "./AuthorizationCompletePage";

describe("isCurrentUser", () => {
  it("accepts the bounded identity needed to confirm a Session", () => {
    expect(
      isCurrentUser({ id: "user-1", username: "student", display_name: "Student" }),
    ).toBe(true);
  });

  it("rejects malformed success data", () => {
    expect(isCurrentUser({ id: "user-1" })).toBe(false);
  });
});
