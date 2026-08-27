import { describe, expect, it } from "vitest";

import type { PublicAccessDiscovery } from "../../auth/PublicAccessDiscovery";
import { resolveInvitationInstitutionName } from "./InvitationApi";

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
  institution: {
    id: "institution-1",
    name: "example-university",
    display_name: "Example University",
  },
  providers: [],
};

describe("resolveInvitationInstitutionName", () => {
  it("uses only validated Institution presentation", () => {
    expect(
      resolveInvitationInstitutionName({ kind: "ready", discovery }),
    ).toBe("Example University");
  });

  it("keeps discovery optional for Invitation acceptance", () => {
    expect(resolveInvitationInstitutionName({ kind: "unavailable" })).toBeUndefined();
    expect(
      resolveInvitationInstitutionName({
        kind: "ready",
        discovery: { ...discovery, institution: undefined },
      }),
    ).toBeUndefined();
  });
});
