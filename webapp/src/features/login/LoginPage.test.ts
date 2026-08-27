import { describe, expect, it } from "vitest";

import type { PublicAccessDiscovery } from "../../auth/PublicAccessDiscovery";
import { resolveLoginDiscovery } from "./LoginDiscovery";

const discovery: PublicAccessDiscovery = {
  discovery_version: 1,
  canonical_origin: "https://proctor.example",
  initialized: true,
  capabilities: {
    local_login: true,
    public_registration: false,
    invitation_admission: true,
    desktop_authorization: true,
  },
  desktop_authorization: {
    protocol: "proctor-desktop-authorization",
    minimum_version: 1,
    maximum_version: 1,
  },
  providers: [],
};

describe("resolveLoginDiscovery", () => {
  it("admits a valid same-origin local-login document", () => {
    expect(resolveLoginDiscovery({ kind: "ready", discovery })).toEqual({
      kind: "ready",
      discovery,
    });
  });

  it("preserves origin mismatch and shared discovery failure", () => {
    expect(resolveLoginDiscovery({ kind: "origin_mismatch" })).toEqual({
      kind: "origin_mismatch",
    });
    expect(resolveLoginDiscovery({ kind: "unavailable" })).toEqual({
      kind: "failure",
    });
  });

  it("keeps setup and unavailable-method decisions feature-owned", () => {
    const setup = { ...discovery, initialized: false };
    expect(resolveLoginDiscovery({ kind: "ready", discovery: setup })).toEqual({
      kind: "setup",
      discovery: setup,
    });

    const unavailable = {
      ...discovery,
      capabilities: { ...discovery.capabilities, local_login: false },
    };
    expect(
      resolveLoginDiscovery({ kind: "ready", discovery: unavailable }),
    ).toEqual({
      kind: "unavailable",
      discovery: unavailable,
    });
  });
});
