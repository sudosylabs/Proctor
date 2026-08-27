import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export type PasswordResetRequestResult =
  | { kind: "accepted" }
  | { kind: "problem"; code?: string }
  | { kind: "failure" };

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
    return { kind: "problem", code: readProblemValue(error)?.code };
  } catch {
    return { kind: "failure" };
  }
}
