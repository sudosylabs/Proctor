import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export type PasswordResetCompletionResult =
  | { kind: "complete" }
  | { kind: "invalid" }
  | { kind: "password_rejected" }
  | { kind: "rate_limited" }
  | { kind: "unavailable" };

export async function completePasswordReset(
  token: string,
  password: string,
): Promise<PasswordResetCompletionResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/password-reset/complete",
      { body: { token, password } },
    );
    if (response.status === 204) {
      return { kind: "complete" };
    }
    switch (readProblemValue(error)?.code) {
      case "authentication.account_token.invalid":
      case "request.invalid":
        return { kind: "invalid" };
      case "authentication.password.invalid":
        return { kind: "password_rejected" };
      case "authentication.rate_limited":
        return { kind: "rate_limited" };
      default:
        return { kind: "unavailable" };
    }
  } catch {
    return { kind: "unavailable" };
  }
}
