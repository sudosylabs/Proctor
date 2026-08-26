import { describe, expect, it } from "vitest";

import { resolveDiscovery } from "./LoginPage";

const discovery = {
  discovery_version: 1,
  canonical_origin: "https://proctor.example",
  initialized: true,
  capabilities: {
    local_login: true,
    public_registration: false,
    invitation_admission: true,
    desktop_authorization: true,
  },
  desktop_authorization: {},
  providers: [],
};

describe("resolveDiscovery", () => {
  it("admits a valid same-origin local-login document", () => {
    expect(resolveDiscovery(discovery, "https://proctor.example")).toEqual({
      kind: "ready",
      discovery,
    });
  });

  it("fails closed when the canonical origin differs", () => {
    expect(resolveDiscovery(discovery, "https://other.example")).toEqual({
      kind: "origin_mismatch",
    });
  });

  it("rejects a malformed success body", () => {
    expect(resolveDiscovery({ ...discovery, providers: [{}] }, "https://proctor.example")).toEqual({
      kind: "failure",
    });
  });
});
