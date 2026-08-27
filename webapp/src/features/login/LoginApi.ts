import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export interface LocalLoginSubmission {
  loginID: string;
  mfaCode?: string;
  password: string;
}

export type LocalLoginResult =
  | { kind: "authenticated" }
  | { kind: "mfa_required" }
  | { kind: "mfa_invalid" }
  | { kind: "invalid_credentials" }
  | { kind: "mfa_unavailable" }
  | { kind: "session_limit" }
  | { kind: "rate_limited" }
  | { kind: "unavailable" };

export type AuthenticateLocal = (
  submission: LocalLoginSubmission,
) => Promise<LocalLoginResult>;

export const authenticateLocal: AuthenticateLocal = async (submission) => {
  try {
    const { data, error, response } = await apiClient.POST("/api/v1/auth/login", {
      body: {
        login_id: submission.loginID,
        password: submission.password,
        client_type: "web",
        ...(submission.mfaCode === undefined
          ? {}
          : { mfa_code: submission.mfaCode }),
      },
    });
    if (
      response.status === 200 &&
      isRecord(data) &&
      isRecord(data.session) &&
      typeof data.session.id === "string" &&
      data.session.client_type === "web"
    ) {
      return { kind: "authenticated" };
    }

    switch (readProblemValue(error)?.code) {
      case "authentication.mfa.required":
        return { kind: "mfa_required" };
      case "authentication.mfa.invalid_code":
        return { kind: "mfa_invalid" };
      case "authentication.invalid_credentials":
        return { kind: "invalid_credentials" };
      case "authentication.mfa.unavailable":
        return { kind: "mfa_unavailable" };
      case "authentication.sessions.maximum_reached":
        return { kind: "session_limit" };
      case "authentication.rate_limited":
        return { kind: "rate_limited" };
      default:
        return { kind: "unavailable" };
    }
  } catch {
    return { kind: "unavailable" };
  }
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
