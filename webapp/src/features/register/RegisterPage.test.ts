import { describe, expect, it } from "vitest";

import { resolveRegistrationDiscovery } from "./RegistrationApi";

const discovery = {
  discovery_version: 1,
  canonical_origin: "https://proctor.example",
  initialized: true,
  capabilities: {
    local_login: true,
    public_registration: true,
    invitation_admission: true,
    desktop_authorization: true,
  },
  desktop_authorization: {},
  institution: {
    id: "institution-1",
    name: "northbridge",
    display_name: "Northbridge Institute",
  },
  providers: [],
} as const;

describe("resolveRegistrationDiscovery", () => {
  it("admits public registration only for the serving origin", () => {
    expect(
      resolveRegistrationDiscovery(discovery, "https://proctor.example"),
    ).toEqual({ kind: "ready", discovery });
    expect(
      resolveRegistrationDiscovery(discovery, "https://other.example"),
    ).toEqual({ kind: "origin_mismatch" });
  });

  it("distinguishes setup and Invitation admission", () => {
    const setup = { ...discovery, initialized: false };
    expect(resolveRegistrationDiscovery(setup, "https://proctor.example")).toEqual({
      kind: "setup",
      discovery: setup,
    });

    const invitationRequired = {
      ...discovery,
      capabilities: { ...discovery.capabilities, public_registration: false },
    };
    expect(
      resolveRegistrationDiscovery(
        invitationRequired,
        "https://proctor.example",
      ),
    ).toEqual({ kind: "invitation_required", discovery: invitationRequired });
  });

  it("rejects malformed discovery", () => {
    expect(
      resolveRegistrationDiscovery(
        { ...discovery, capabilities: {} },
        "https://proctor.example",
      ),
    ).toEqual({ kind: "failure" });
  });
});
