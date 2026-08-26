import { describe, expect, it } from "vitest";

import { resolveInstallationStatus } from "./SetupApi";

describe("resolveInstallationStatus", () => {
  it("opens setup only before initialization", () => {
    expect(resolveInstallationStatus({ initialized: false })).toEqual({
      kind: "ready",
    });
    expect(resolveInstallationStatus({ initialized: true })).toEqual({
      kind: "complete",
    });
  });

  it("rejects a malformed public status", () => {
    expect(resolveInstallationStatus({ initialized: "false" })).toEqual({
      kind: "failure",
    });
    expect(resolveInstallationStatus(undefined)).toEqual({ kind: "failure" });
  });
});
