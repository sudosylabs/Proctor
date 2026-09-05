import { afterEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../api/client";
import type { PublicAccessDiscovery } from "../../auth/PublicAccessDiscovery";
import { apiResult } from "../../test/ApiClientTestAdapter";
import { approveDesktopAuthorization, authenticateDesktopAuthorizationLocally, requestDesktopAuthorizationContext } from "./DesktopAuthorizationApi";

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

  it("combines the browser transaction with validated public discovery", async () => {
    vi.spyOn(apiClient, "GET").mockResolvedValue(
      apiResult(200, {
        data: {
          state: "authenticated",
          account: {
            id: "user-1",
            username: "student.one",
            display_name: "Student One",
          },
          device_name: "Exam laptop",
          expires_at: 1000,
          local_login_enabled: false,
          external_providers: [],
        },
      }),
    );
    await expect(
      requestDesktopAuthorizationContext(
        "https://proctor.example",
        async () => ({ kind: "ready", discovery }),
      ),
    ).resolves.toEqual({
      kind: "ready",
      context: {
        account: {
          id: "user-1",
          username: "student.one",
          display_name: "Student One",
        },
        deviceName: "Exam laptop",
        expiresAt: 1000,
        externalProviders: [],
        installation: "Example University",
        localLoginEnabled: false,
        state: "authenticated",
      },
    });
  });

  it("distinguishes a rejected binding from unavailable public context", async () => {
    const get = vi.spyOn(apiClient, "GET");
    get.mockResolvedValue(
      apiResult(409, {
        problemCode: "authentication.desktop_authorization.rejected",
      }),
    );
    await expect(
      requestDesktopAuthorizationContext(
        "https://proctor.example",
        async () => ({ kind: "ready", discovery }),
      ),
    ).resolves.toEqual({ kind: "invalid" });

    get.mockResolvedValue(
      apiResult(200, {
        data: {
          state: "bound",
          device_name: "",
          expires_at: 1000,
          local_login_enabled: true,
          external_providers: [],
        },
      }),
    );
    await expect(
      requestDesktopAuthorizationContext(
        "https://proctor.example",
        async () => ({ kind: "origin_mismatch" }),
      ),
    ).resolves.toEqual({ kind: "unavailable" });
  });

  it.each([
    { external_providers: [null] },
    { external_providers: [{}] },
    { external_providers: [{ id: "campus", display_name: null, type: "oidc" }] },
    { external_providers: [{ id: "../campus", display_name: "Campus", type: "oidc" }] },
    { external_providers: [{ id: "campus", display_name: "Campus", type: "oidc" }, { id: "campus", display_name: "Campus", type: "oidc" }] },
    { external_providers: Array.from({ length: 65 }, (_, index) => ({ id: `campus-${index}`, display_name: "Campus", type: "oidc" })) },
    { expires_at: Number.NaN },
    { expires_at: Number.POSITIVE_INFINITY },
    { expires_at: 1.5 },
    { device_name: "x".repeat(129) },
    { account: { id: "unexpected", username: "student", display_name: "Student" } },
    { state: "authenticated", account: { id: "user", username: "", display_name: "Student" } },
  ])("rejects malformed context before presentation: %j", async (override) => {
    vi.spyOn(apiClient, "GET").mockResolvedValue(apiResult(200, { data: {
      state: "bound", device_name: "Laptop", expires_at: 1000, local_login_enabled: true,
      external_providers: [], ...override,
    } }));
    await expect(requestDesktopAuthorizationContext("https://proctor.example", async () => ({ kind: "ready", discovery })))
      .resolves.toEqual({ kind: "unavailable" });
  });

  it("requires authentication success to carry an authenticated context", async () => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200, { data: {
      state: "bound", device_name: "Laptop", expires_at: 1000, local_login_enabled: true, external_providers: [],
    } }));
    await expect(authenticateDesktopAuthorizationLocally("Example University", { loginID: "student", password: "private" }))
      .resolves.toEqual({ kind: "unavailable" });
  });
});

describe("Desktop approval navigation", () => {
  afterEach(() => vi.restoreAllMocks());
  const path = "A".repeat(43);
  const code = "B".repeat(43);
  const query = `code=${code}&state=desktop-state`;

  it.each(["127.0.0.1:55000", "[::1]:55000", "127.0.0.1:055000", "[::1]:055000"])("accepts the exact %s loopback protocol", async (host) => {
    const redirectURL = `http://${host}/${path}?${query}`;
    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200, { data: { redirect_url: redirectURL, expires_at: 1000 } }));
    await expect(approveDesktopAuthorization("desktop-state")).resolves.toEqual({ kind: "approved", redirectURL });
  });

  it("accepts a maximum-size registered callback with its terminal query", async () => {
    const prefix = "http://127.0.0.1:";
    const suffix = `55000/${path}`;
    const callback = `${prefix}${"0".repeat(1024 - prefix.length - suffix.length)}${suffix}`;
    const redirectURL = `${callback}?${query}`;
    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200, { data: { redirect_url: redirectURL, expires_at: 1000 } }));
    await expect(approveDesktopAuthorization("desktop-state")).resolves.toEqual({ kind: "approved", redirectURL });
  });

  it.each([
    `https://other.example/${path}?${query}`,
    `http://localhost:55000/${path}?${query}`,
    `http://127.1:55000/${path}?${query}`,
    `http://127.0.0.1:80/${path}?${query}`,
    `http://127.0.0.1:049151/${path}?${query}`,
    `http://127.0.0.1:065536/${path}?${query}`,
    `http://user@127.0.0.1:55000/${path}?${query}`,
    `http://127.0.0.1:55000/${path}?${query}#fragment`,
    `http://127.0.0.1:55000/callback?${query}`,
    `http://127.0.0.1:55000/${path}?code=${code}&state=other`,
    `http://127.0.0.1:55000/${path}?${query}&state=desktop-state`,
    `http://127.0.0.1:55000/${path}?${query}&access_token=private`,
    `http://127.0.0.1:55000/${path}?state=desktop-state`,
    "javascript:alert(1)",
  ])("rejects invalid navigation result %s", async (redirectURL) => {
    vi.spyOn(apiClient, "POST").mockResolvedValue(apiResult(200, { data: { redirect_url: redirectURL, expires_at: 1000 } }));
    await expect(approveDesktopAuthorization("desktop-state")).resolves.toEqual({ kind: "unavailable" });
  });
});
