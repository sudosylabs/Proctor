import { apiClient } from "../../api/client";
import type { components } from "../../api/generated/schema";
import { readProblemValue } from "../../api/problem";

export type InvitationTransaction =
  components["schemas"]["BrowserInvitationStartResponse"];

export type InvitationStartResult =
  | { kind: "ready"; transaction: InvitationTransaction }
  | { kind: "invalid" }
  | { kind: "unavailable" };

export interface InvitationAccountSubmission {
  firstName: string;
  handle: string;
  lastName: string;
  password: string;
  username: string;
}

export type InvitationAccountAcceptanceResult =
  | { kind: "accepted" }
  | { kind: "password_rejected" }
  | { kind: "details_invalid" }
  | { kind: "rate_limited" }
  | { kind: "unavailable" };

export type InvitationSessionAcceptanceResult =
  | { kind: "accepted" }
  | { kind: "session_required" }
  | { kind: "unavailable" };

export async function startInvitation(
  claim: string,
): Promise<InvitationStartResult> {
  try {
    const { data, error, response } = await apiClient.POST(
      "/api/v1/auth/browser/invitations",
      { body: { claim } },
    );
    if (response.status === 201 && isInvitationTransaction(data)) {
      return { kind: "ready", transaction: data };
    }
    const code = readProblemValue(error)?.code;
    if (code === "invitation.invalid" || code === "request.invalid") {
      return { kind: "invalid" };
    }
    return { kind: "unavailable" };
  } catch {
    return { kind: "unavailable" };
  }
}

export async function requestInvitationInstitutionName(): Promise<
  string | undefined
> {
  try {
    const { data, response } = await apiClient.GET("/api/v1/discovery");
    if (
      response.status === 200 &&
      isRecord(data) &&
      isRecord(data.institution) &&
      typeof data.institution.display_name === "string" &&
      data.institution.display_name !== ""
    ) {
      return data.institution.display_name;
    }
  } catch {
    // The Invitation remains usable without optional public presentation.
  }
  return undefined;
}

export async function acceptInvitationAccount(
  submission: InvitationAccountSubmission,
): Promise<InvitationAccountAcceptanceResult> {
  try {
    const firstName = submission.firstName.trim();
    const lastName = submission.lastName.trim();
    const { data, error, response } = await apiClient.POST(
      "/api/v1/auth/browser/invitations/accept",
      {
        body: {
          handle: submission.handle,
          username: submission.username.trim(),
          password: submission.password,
          ...(firstName === "" ? {} : { first_name: firstName }),
          ...(lastName === "" ? {} : { last_name: lastName }),
        },
      },
    );
    if (response.status === 200 && isInvitationAcceptance(data)) {
      return { kind: "accepted" };
    }
    switch (readProblemValue(error)?.code) {
      case "authentication.password.invalid":
        return { kind: "password_rejected" };
      case "invitation.invalid":
      case "invitation.user_invalid":
        return { kind: "details_invalid" };
      case "authentication.rate_limited":
        return { kind: "rate_limited" };
      default:
        return { kind: "unavailable" };
    }
  } catch {
    return { kind: "unavailable" };
  }
}

export async function acceptInvitationSession(
  handle: string,
): Promise<InvitationSessionAcceptanceResult> {
  try {
    const { data, error, response } = await apiClient.POST(
      "/api/v1/auth/browser/invitations/accept-session",
      { body: { handle } },
    );
    if (response.status === 200 && isInvitationAcceptance(data)) {
      return { kind: "accepted" };
    }
    switch (readProblemValue(error)?.code) {
      case "authentication.required":
      case "authentication.invalid_token":
        return { kind: "session_required" };
      default:
        return { kind: "unavailable" };
    }
  } catch {
    return { kind: "unavailable" };
  }
}

function isInvitationTransaction(
  value: unknown,
): value is InvitationTransaction {
  return (
    isRecord(value) &&
    typeof value.handle === "string" &&
    (value.requirement === "account" || value.requirement === "session") &&
    (value.purpose === "student_class" ||
      value.purpose === "teacher_academic_unit" ||
      value.purpose === "academic_unit_role" ||
      value.purpose === "institution_role") &&
    typeof value.expires_at === "number"
  );
}

function isInvitationAcceptance(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.invitation_id === "string" &&
    typeof value.user_id === "string" &&
    typeof value.replayed === "boolean"
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
