import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export type EmailVerificationResult =
  | { kind: "verified" }
  | { kind: "invalid" }
  | { kind: "unavailable" };

export function resolveEmailVerificationResult(
  response: Pick<Response, "status">,
  error: unknown,
): EmailVerificationResult {
  if (response.status === 204) {
    return { kind: "verified" };
  }

  const code = readProblemValue(error)?.code;
  if (
    code === "authentication.account_token.invalid" ||
    code === "request.invalid"
  ) {
    return { kind: "invalid" };
  }
  return { kind: "unavailable" };
}

export async function completeEmailVerification(
  token: string,
): Promise<EmailVerificationResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/email-verification/complete",
      { body: { token } },
    );
    return resolveEmailVerificationResult(response, error);
  } catch {
    return { kind: "unavailable" };
  }
}
