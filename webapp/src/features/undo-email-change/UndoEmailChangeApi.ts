import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export type EmailChangeUndoResult =
  | { kind: "restored" }
  | { kind: "invalid" }
  | { kind: "conflict" }
  | { kind: "rate_limited" }
  | { kind: "unavailable" };

export function resolveEmailChangeUndoResult(
  response: Pick<Response, "status">,
  error: unknown,
): EmailChangeUndoResult {
  if (response.status === 204) {
    return { kind: "restored" };
  }
  switch (readProblemValue(error)?.code) {
    case "authentication.account_token.invalid":
    case "request.invalid":
      return { kind: "invalid" };
    case "authentication.email_change_undo.conflict":
      return { kind: "conflict" };
    case "authentication.rate_limited":
      return { kind: "rate_limited" };
    default:
      return { kind: "unavailable" };
  }
}

export async function undoEmailChange(
  token: string,
): Promise<EmailChangeUndoResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/email-change/undo",
      { body: { token } },
    );
    return resolveEmailChangeUndoResult(response, error);
  } catch {
    return { kind: "unavailable" };
  }
}
