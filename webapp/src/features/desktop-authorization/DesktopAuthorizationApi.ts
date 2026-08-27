import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export interface DesktopAuthorizationContext {
  account: string;
  installation: string;
}

export type DesktopContextResult =
  | { kind: "ready"; context: DesktopAuthorizationContext }
  | { kind: "no_session" }
  | { kind: "unavailable" };

export interface DesktopAuthorizationProof {
  browserProof: string;
  handle: string;
  state: string;
}

export type DesktopApprovalResult =
  | { kind: "approved"; redirectURL: string }
  | { kind: "no_session" }
  | { kind: "invalid" }
  | { kind: "unavailable" };

export type DesktopCancellationResult =
  | { kind: "cancelled" }
  | { kind: "invalid" }
  | { kind: "unavailable" };

export async function requestDesktopAuthorizationContext(): Promise<DesktopContextResult> {
  try {
    const [userResult, discoveryResult] = await Promise.all([
      apiClient.GET("/api/v1/users/me"),
      apiClient.GET("/api/v1/discovery"),
    ]);
    if (userResult.response.status === 401) {
      return { kind: "no_session" };
    }
    if (
      userResult.response.status !== 200 ||
      discoveryResult.response.status !== 200 ||
      !isRecord(userResult.data) ||
      typeof userResult.data.username !== "string" ||
      userResult.data.username === "" ||
      !isRecord(discoveryResult.data) ||
      !isRecord(discoveryResult.data.institution) ||
      typeof discoveryResult.data.institution.display_name !== "string" ||
      discoveryResult.data.institution.display_name === ""
    ) {
      return { kind: "unavailable" };
    }
    return {
      kind: "ready",
      context: {
        account: userResult.data.username,
        installation: discoveryResult.data.institution.display_name,
      },
    };
  } catch {
    return { kind: "unavailable" };
  }
}

export async function approveDesktopAuthorization(
  proof: DesktopAuthorizationProof,
): Promise<DesktopApprovalResult> {
  try {
    const { data, error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/approve",
      {
        body: {
          handle: proof.handle,
          browser_proof: proof.browserProof,
          state: proof.state,
        },
      },
    );
    if (
      response.status === 200 &&
      isRecord(data) &&
      typeof data.redirect_url === "string"
    ) {
      return { kind: "approved", redirectURL: data.redirect_url };
    }
    const code = readProblemValue(error)?.code;
    if (
      code === "authentication.required" ||
      code === "authentication.invalid_token"
    ) {
      return { kind: "no_session" };
    }
    if (
      code === "authentication.desktop_authorization.invalid" ||
      code === "authentication.desktop_authorization.rejected" ||
      code === "request.invalid"
    ) {
      return { kind: "invalid" };
    }
    return { kind: "unavailable" };
  } catch {
    return { kind: "unavailable" };
  }
}

export async function cancelDesktopAuthorization(
  proof: DesktopAuthorizationProof,
): Promise<DesktopCancellationResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/cancel",
      {
        body: {
          handle: proof.handle,
          browser_proof: proof.browserProof,
          state: proof.state,
        },
      },
    );
    if (response.status === 204) {
      return { kind: "cancelled" };
    }
    const code = readProblemValue(error)?.code;
    if (
      code === "authentication.desktop_authorization.invalid" ||
      code === "authentication.desktop_authorization.rejected" ||
      code === "request.invalid"
    ) {
      return { kind: "invalid" };
    }
    return { kind: "unavailable" };
  } catch {
    return { kind: "unavailable" };
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
