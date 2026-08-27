import { apiClient } from "../api/client";
import type { components } from "../api/generated/schema";

export type PublicAccessDiscovery =
  components["schemas"]["PublicAccessDiscoveryResponse"];

export type PublicAccessDiscoveryResult =
  | { kind: "ready"; discovery: PublicAccessDiscovery }
  | { kind: "origin_mismatch" }
  | { kind: "unavailable" };

export interface PublicAccessDiscoveryTransport {
  request(): Promise<{ data: unknown; status: number }>;
}

export type PublicAccessDiscoveryLoader = (
  servingOrigin: string,
) => Promise<PublicAccessDiscoveryResult>;

const httpTransport: PublicAccessDiscoveryTransport = {
  async request() {
    const { data, response } = await apiClient.GET("/api/v1/discovery");
    return { data, status: response.status };
  },
};

export function createPublicAccessDiscoveryLoader(
  transport: PublicAccessDiscoveryTransport,
): PublicAccessDiscoveryLoader {
  return async (servingOrigin) => {
    try {
      const response = await transport.request();
      if (response.status !== 200) {
        return { kind: "unavailable" };
      }
      return resolvePublicAccessDiscovery(response.data, servingOrigin);
    } catch {
      return { kind: "unavailable" };
    }
  };
}

export const requestPublicAccessDiscovery =
  createPublicAccessDiscoveryLoader(httpTransport);

export function resolvePublicAccessDiscovery(
  value: unknown,
  servingOrigin: string,
): PublicAccessDiscoveryResult {
  if (!isPublicAccessDiscovery(value)) {
    return { kind: "unavailable" };
  }

  let canonicalOrigin: string;
  try {
    canonicalOrigin = new URL(value.canonical_origin).origin;
  } catch {
    return { kind: "unavailable" };
  }
  if (canonicalOrigin !== servingOrigin) {
    return { kind: "origin_mismatch" };
  }
  return { kind: "ready", discovery: value };
}

function isPublicAccessDiscovery(
  value: unknown,
): value is PublicAccessDiscovery {
  if (
    !isRecord(value) ||
    value.discovery_version !== 1 ||
    typeof value.canonical_origin !== "string" ||
    typeof value.initialized !== "boolean" ||
    !isCapabilities(value.capabilities) ||
    !isDesktopAuthorizationCompatibility(value.desktop_authorization) ||
    !Array.isArray(value.providers) ||
    !value.providers.every(isProvider) ||
    (value.installation_id !== undefined &&
      typeof value.installation_id !== "string") ||
    (value.policy_revision !== undefined &&
      typeof value.policy_revision !== "number")
  ) {
    return false;
  }
  return value.institution === undefined || isInstitution(value.institution);
}

function isCapabilities(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.local_login === "boolean" &&
    typeof value.public_registration === "boolean" &&
    typeof value.invitation_admission === "boolean" &&
    typeof value.desktop_authorization === "boolean"
  );
}

function isDesktopAuthorizationCompatibility(value: unknown): boolean {
  return (
    isRecord(value) &&
    value.protocol === "proctor-desktop-authorization" &&
    typeof value.minimum_version === "number" &&
    typeof value.maximum_version === "number"
  );
}

function isInstitution(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    typeof value.name === "string" &&
    typeof value.display_name === "string"
  );
}

function isProvider(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    value.id !== "" &&
    typeof value.display_name === "string" &&
    value.display_name !== "" &&
    typeof value.type === "string"
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
