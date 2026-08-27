import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export type PasswordResetRequestResult =
  | { kind: "accepted" }
  | { kind: "rate_limited" }
  | { kind: "unavailable" };

export async function requestPasswordReset(
  email: string,
): Promise<PasswordResetRequestResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/password-reset/request",
      { body: { email: email.trim() } },
    );
    if (response.status === 202) {
      return { kind: "accepted" };
    }
    if (readProblemValue(error)?.code === "authentication.rate_limited") {
      return { kind: "rate_limited" };
    }
    return { kind: "unavailable" };
  } catch {
    return { kind: "unavailable" };
  }
}
