import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export type PasswordResetCompletionResult =
  | { kind: "complete" }
  | { kind: "problem"; code?: string }
  | { kind: "failure" };

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
    return { kind: "problem", code: readProblemValue(error)?.code };
  } catch {
    return { kind: "failure" };
  }
}
