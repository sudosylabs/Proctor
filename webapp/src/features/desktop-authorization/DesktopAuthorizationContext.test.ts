import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import type { PublicAccessDiscovery } from "../../auth/PublicAccessDiscovery";
import { apiResult } from "../../test/ApiClientTestAdapter";
import { requestDesktopAuthorizationContext } from "./DesktopAuthorizationApi";

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

describe("requestDesktopAuthorizationContext", () => {
  afterEach(() => vi.restoreAllMocks());

  it("combines the current User with validated public discovery", async () => {
    vi.spyOn(apiClient, "GET").mockResolvedValue(
      apiResult(200, { data: { username: "student.one" } }),
    );
    await expect(
      requestDesktopAuthorizationContext(
        "https://proctor.example",
        async () => ({ kind: "ready", discovery }),
      ),
    ).resolves.toEqual({
      kind: "ready",
      context: {
        account: "student.one",
        installation: "Example University",
      },
    });
  });

  it("distinguishes a missing Session from unavailable public context", async () => {
    const get = vi.spyOn(apiClient, "GET");
    get.mockResolvedValue(apiResult(401));
    await expect(
      requestDesktopAuthorizationContext(
        "https://proctor.example",
        async () => ({ kind: "ready", discovery }),
      ),
    ).resolves.toEqual({ kind: "no_session" });

    get.mockResolvedValue(apiResult(200, { data: { username: "student.one" } }));
    await expect(
      requestDesktopAuthorizationContext(
        "https://proctor.example",
        async () => ({ kind: "origin_mismatch" }),
      ),
    ).resolves.toEqual({ kind: "unavailable" });
  });
});
