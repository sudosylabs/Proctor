import { apiClient } from "../../api/client";
import type { components } from "../../api/generated/schema";
import { readProblemValue } from "../../api/problem";

export type Discovery = components["schemas"]["PublicAccessDiscoveryResponse"];

export type RegistrationDiscoveryState =
  | { kind: "loading" }
  | { kind: "ready"; discovery: Discovery }
  | { kind: "setup"; discovery: Discovery }
  | { kind: "invitation_required"; discovery: Discovery }
  | { kind: "unavailable"; discovery?: Discovery }
  | { kind: "origin_mismatch" }
  | { kind: "failure" };

export interface RegistrationSubmission {
  email: string;
  firstName: string;
  lastName: string;
  username: string;
  password: string;
}

export type RegistrationSubmissionResult =
  | { kind: "accepted" }
  | { kind: "problem"; code?: string }
  | { kind: "failure" };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isRegistrationDiscovery(value: unknown): value is Discovery {
  if (
    !isRecord(value) ||
    value.discovery_version !== 1 ||
    typeof value.canonical_origin !== "string" ||
    typeof value.initialized !== "boolean" ||
    !isRecord(value.capabilities) ||
    typeof value.capabilities.public_registration !== "boolean"
  ) {
    return false;
  }
  return (
    value.institution === undefined ||
    (isRecord(value.institution) &&
      typeof value.institution.id === "string" &&
      typeof value.institution.name === "string" &&
      typeof value.institution.display_name === "string")
  );
}

export function resolveRegistrationDiscovery(
  value: unknown,
  servingOrigin: string,
): RegistrationDiscoveryState {
  if (!isRegistrationDiscovery(value)) {
    return { kind: "failure" };
  }

  let canonicalOrigin: string;
  try {
    canonicalOrigin = new URL(value.canonical_origin).origin;
  } catch {
    return { kind: "failure" };
  }
  if (canonicalOrigin !== servingOrigin) {
    return { kind: "origin_mismatch" };
  }
  if (!value.initialized) {
    return { kind: "setup", discovery: value };
  }
  if (!value.capabilities.public_registration) {
    return { kind: "invitation_required", discovery: value };
  }
  return { kind: "ready", discovery: value };
}

export async function requestRegistrationDiscovery(
  servingOrigin: string,
): Promise<RegistrationDiscoveryState> {
  try {
    const { data } = await apiClient.GET("/api/v1/discovery");
    return resolveRegistrationDiscovery(data, servingOrigin);
  } catch {
    return { kind: "failure" };
  }
}

export async function submitRegistration(
  submission: RegistrationSubmission,
): Promise<RegistrationSubmissionResult> {
  try {
    const { error, response } = await apiClient.POST("/api/v1/auth/register", {
      body: {
        email: submission.email.trim(),
        first_name: submission.firstName.trim(),
        last_name: submission.lastName.trim(),
        username: submission.username.trim(),
        password: submission.password,
      },
    });

    if (response.ok) {
      return { kind: "accepted" };
    }
    return { kind: "problem", code: readProblemValue(error)?.code };
  } catch {
    return { kind: "failure" };
  }
}
