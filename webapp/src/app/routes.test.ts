import { describe, expect, it } from "vitest";

import { isHostedRoute } from "./routes";

describe("hosted route catalog", () => {
  it("accepts only declared server-hosted pages", () => {
    expect(isHostedRoute("/login")).toBe(true);
    expect(isHostedRoute("/account/reset-password")).toBe(true);
    expect(isHostedRoute("/")).toBe(false);
    expect(isHostedRoute("/api/v1/discovery")).toBe(false);
    expect(isHostedRoute("/assets/index.js")).toBe(false);
  });
});
