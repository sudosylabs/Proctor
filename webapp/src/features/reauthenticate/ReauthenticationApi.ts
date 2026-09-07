import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";
import { boundedText, isRecord, isWebSession, securityFailure, type SecurityFailure, type SecurityMutationResult } from "../account-security/AccountSecurityApi";

export type ReauthenticationTask = "security" | "connect-provider";
export type ReauthenticationFailure = SecurityFailure | { kind: "invalid_password" } | { kind: "method_required" };
export type PasswordProofResult = { kind: "success" } | ReauthenticationFailure;
export type ExternalProofResult = { kind: "redirect"; url: string } | ReauthenticationFailure;

export interface ReauthenticationActions {
  password(value: string): Promise<PasswordProofResult>;
  external(task: ReauthenticationTask): Promise<ExternalProofResult>;
  challenge(code: string): Promise<SecurityMutationResult>;
}

export function reauthenticationDestination(task: ReauthenticationTask): string {
  return task === "connect-provider" ? "/account/connect-provider" : "/account/security";
}

export async function proveCurrentPassword(password: string): Promise<PasswordProofResult> {
  try {
    const { data, error, response } = await apiClient.POST("/api/v1/auth/reauthenticate/password", { body: { password } });
    if (response.status === 200 && isWebSession(data) && isRecord(data) &&
      typeof data.reauthenticated_at === "number" && Number.isSafeInteger(data.reauthenticated_at) && data.reauthenticated_at > 0) {
      return { kind: "success" };
    }
    return proofFailure(error, response.status);
  } catch { return { kind: "unavailable" }; }
}

export async function beginExternalProof(task: ReauthenticationTask): Promise<ExternalProofResult> {
  try {
    const { data, error, response } = await apiClient.POST("/api/v1/auth/reauthenticate/external", { body: { task } });
    if (response.status === 200 && isRecord(data) && boundedText(data.redirect_url, 16384)) {
      const target = new URL(data.redirect_url);
      if ((target.protocol === "https:" || target.protocol === "http:") && target.username === "" && target.password === "") {
        return { kind: "redirect", url: target.href };
      }
    }
    return proofFailure(error, response.status);
  } catch { return { kind: "unavailable" }; }
}

function proofFailure(error: unknown, status: number): ReauthenticationFailure {
  switch (readProblemValue(error)?.code) {
    case "authentication.invalid_credentials": return { kind: "invalid_password" };
    case "authentication.reauthentication_method_required": return { kind: "method_required" };
    default: return securityFailure(error, status);
  }
}
