import { describe, expect, it } from "vitest";

import {
  resolveRegistrationDiscovery,
  type Discovery,
} from "./RegistrationApi";

const discovery: Discovery = {
  discovery_version: 1,
  canonical_origin: "https://proctor.example",
  initialized: true,
  capabilities: {
    local_login: true,
    public_registration: true,
    invitation_admission: true,
    desktop_authorization: true,
  },
  desktop_authorization: {
    protocol: "proctor-desktop-authorization",
    minimum_version: 1,
    maximum_version: 1,
  },
  institution: {
    id: "institution-1",
    name: "northbridge",
    display_name: "Northbridge Institute",
  },
  providers: [],
};

describe("resolveRegistrationDiscovery", () => {
  it("admits public registration only for the serving origin", () => {
    expect(
      resolveRegistrationDiscovery({ kind: "ready", discovery }),
    ).toEqual({ kind: "ready", discovery });
    expect(
      resolveRegistrationDiscovery({ kind: "origin_mismatch" }),
    ).toEqual({ kind: "origin_mismatch" });
  });

  it("distinguishes setup and Invitation admission", () => {
    const setup = { ...discovery, initialized: false };
    expect(resolveRegistrationDiscovery({ kind: "ready", discovery: setup })).toEqual({
      kind: "setup",
      discovery: setup,
    });

    const invitationRequired = {
      ...discovery,
      capabilities: { ...discovery.capabilities, public_registration: false },
    };
    expect(
      resolveRegistrationDiscovery(
        { kind: "ready", discovery: invitationRequired },
      ),
    ).toEqual({ kind: "invitation_required", discovery: invitationRequired });
  });

  it("fails safely when shared discovery is unavailable", () => {
    expect(resolveRegistrationDiscovery({ kind: "unavailable" })).toEqual({
      kind: "failure",
    });
  });
});
