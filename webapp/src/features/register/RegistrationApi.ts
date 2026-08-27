import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";
import {
  requestPublicAccessDiscovery,
  type PublicAccessDiscovery,
  type PublicAccessDiscoveryResult,
} from "../../auth/PublicAccessDiscovery";

export type Discovery = PublicAccessDiscovery;

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
  | { kind: "password_rejected" }
  | { kind: "details_invalid" }
  | { kind: "invitation_required" }
  | { kind: "admission_unavailable" }
  | { kind: "rate_limited" }
  | { kind: "unavailable" };

export function resolveRegistrationDiscovery(
  result: PublicAccessDiscoveryResult,
): RegistrationDiscoveryState {
  if (result.kind === "unavailable") {
    return { kind: "failure" };
  }
  if (result.kind === "origin_mismatch") {
    return result;
  }
  const { discovery } = result;
  if (!discovery.initialized) {
    return { kind: "setup", discovery };
  }
  if (!discovery.capabilities.public_registration) {
    return { kind: "invitation_required", discovery };
  }
  return { kind: "ready", discovery };
}

export async function requestRegistrationDiscovery(
  servingOrigin: string,
): Promise<RegistrationDiscoveryState> {
  return resolveRegistrationDiscovery(
    await requestPublicAccessDiscovery(servingOrigin),
  );
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

    if (response.status === 202) {
      return { kind: "accepted" };
    }
    switch (readProblemValue(error)?.code) {
      case "authentication.password.invalid":
        return { kind: "password_rejected" };
      case "authentication.registration.invalid":
        return { kind: "details_invalid" };
      case "authentication.registration.invitation_required":
        return { kind: "invitation_required" };
      case "authentication.registration.unavailable":
        return { kind: "admission_unavailable" };
      case "authentication.rate_limited":
        return { kind: "rate_limited" };
      default:
        return { kind: "unavailable" };
    }
  } catch {
    return { kind: "unavailable" };
  }
}
