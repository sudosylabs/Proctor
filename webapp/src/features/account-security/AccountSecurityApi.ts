import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";

export interface SecurityContext {
  enabled: boolean;
  pending: boolean;
  pendingExpiresAt?: number;
  recoveryCodesRemaining: number;
  serviceEnabled: boolean;
  recoveryRequired: boolean;
  authenticationMethod: string;
  authenticationProviderID?: string;
  authenticationStrength: string;
  recentlyAuthenticated: boolean;
}

export type SecurityFailure = {
  kind:
    | "no_session"
    | "primary_required"
    | "strong_required"
    | "invalid_code"
    | "rate_limited"
    | "disabled"
    | "conflict"
    | "unavailable";
};
export type SecurityContextResult =
  | { kind: "ready"; context: SecurityContext }
  | SecurityFailure;
export interface AuthenticatorSetup { secret: string; expiresAt: number }
export type SetupResult = { kind: "success"; setup: AuthenticatorSetup } | SecurityFailure;
export type RecoveryCodesResult = { kind: "success"; codes: string[] } | SecurityFailure;
export type SecurityMutationResult = { kind: "success" } | SecurityFailure;

export interface SecurityActions {
  setup(): Promise<SetupResult>;
  activate(code: string): Promise<RecoveryCodesResult>;
  challenge(code: string): Promise<SecurityMutationResult>;
  regenerate(): Promise<RecoveryCodesResult>;
  disable(): Promise<SecurityMutationResult>;
  signOut(): Promise<SecurityMutationResult>;
}

export async function requestSecurityContext(): Promise<SecurityContextResult> {
  try {
    const { data, error, response } = await apiClient.GET("/api/v1/users/me/mfa");
    const context = readSecurityContext(data);
    return response.status === 200 && context !== undefined
      ? { kind: "ready", context }
      : securityFailure(error, response.status);
  } catch { return { kind: "unavailable" }; }
}

export async function beginAuthenticatorSetup(): Promise<SetupResult> {
  try {
    const { data, error, response } = await apiClient.POST("/api/v1/users/me/mfa/setup");
    if (response.status === 201 && isRecord(data) && boundedText(data.secret, 256) && positiveTimestamp(data.expires_at)) {
      return { kind: "success", setup: { secret: data.secret, expiresAt: data.expires_at } };
    }
    return securityFailure(error, response.status);
  } catch { return { kind: "unavailable" }; }
}

export async function activateAuthenticator(code: string): Promise<RecoveryCodesResult> {
  try {
    const result = await apiClient.POST("/api/v1/users/me/mfa/activate", { body: { code } });
    return recoveryCodesResult(result.data, result.error, result.response.status);
  } catch { return { kind: "unavailable" }; }
}

export async function regenerateRecoveryCodes(): Promise<RecoveryCodesResult> {
  try {
    const result = await apiClient.POST("/api/v1/users/me/mfa/recovery-codes/regenerate");
    return recoveryCodesResult(result.data, result.error, result.response.status);
  } catch { return { kind: "unavailable" }; }
}

export async function challengeSession(code: string): Promise<SecurityMutationResult> {
  try {
    const { data, error, response } = await apiClient.POST("/api/v1/users/me/mfa/challenge", { body: { code } });
    return response.status === 200 && isWebSession(data)
      ? { kind: "success" }
      : securityFailure(error, response.status);
  } catch { return { kind: "unavailable" }; }
}

export async function disableAuthenticator(): Promise<SecurityMutationResult> {
  try {
    const { error, response } = await apiClient.POST("/api/v1/users/me/mfa/disable");
    return response.status === 204 ? { kind: "success" } : securityFailure(error, response.status);
  } catch { return { kind: "unavailable" }; }
}

export async function signOutSecuritySession(): Promise<SecurityMutationResult> {
  try {
    const { error, response } = await apiClient.POST("/api/v1/auth/logout");
    return response.status === 204 ? { kind: "success" } : securityFailure(error, response.status);
  } catch { return { kind: "unavailable" }; }
}

export function securityFailure(error: unknown, status: number): SecurityFailure {
  switch (readProblemValue(error)?.code) {
    case "authentication.required":
    case "authentication.invalid_token": return { kind: "no_session" };
    case "authentication.reauthentication_required": return { kind: "primary_required" };
    case "authentication.strong_required": return { kind: "strong_required" };
    case "authentication.mfa.invalid_code": return { kind: "invalid_code" };
    case "authentication.rate_limited": return { kind: "rate_limited" };
    case "authentication.mfa.disabled": return { kind: "disabled" };
    case "authentication.mfa.conflict":
    case "authentication.mfa.not_found": return { kind: "conflict" };
    default: return { kind: status === 429 ? "rate_limited" : "unavailable" };
  }
}

export function isWebSession(value: unknown): boolean {
  return isRecord(value) && boundedText(value.id, 128) && value.client_type === "web";
}

function recoveryCodesResult(data: unknown, error: unknown, status: number): RecoveryCodesResult {
  if (status === 200 && isRecord(data) && Array.isArray(data.recovery_codes) &&
    data.recovery_codes.length > 0 && data.recovery_codes.length <= 100 &&
    data.recovery_codes.every((code) => boundedText(code, 256)) &&
    new Set(data.recovery_codes).size === data.recovery_codes.length) {
    return { kind: "success", codes: data.recovery_codes };
  }
  return securityFailure(error, status);
}

function readSecurityContext(value: unknown): SecurityContext | undefined {
  if (!isRecord(value) || typeof value.enabled !== "boolean" || typeof value.pending !== "boolean" ||
    !nonnegativeInteger(value.recovery_codes_remaining) ||
    typeof value.service_enabled !== "boolean" || typeof value.mfa_recovery_required !== "boolean" ||
    !boundedText(value.authentication_method, 128) || !boundedText(value.authentication_strength, 128) ||
    typeof value.recently_authenticated !== "boolean" ||
    (value.pending_expires_at !== undefined && !positiveTimestamp(value.pending_expires_at)) ||
    (value.authentication_provider_id !== undefined && !boundedText(value.authentication_provider_id, 256))) {
    return undefined;
  }
  return {
    enabled: value.enabled, pending: value.pending,
    recoveryCodesRemaining: value.recovery_codes_remaining,
    serviceEnabled: value.service_enabled, recoveryRequired: value.mfa_recovery_required,
    authenticationMethod: value.authentication_method,
    authenticationStrength: value.authentication_strength,
    recentlyAuthenticated: value.recently_authenticated,
    ...(value.pending_expires_at === undefined ? {} : { pendingExpiresAt: value.pending_expires_at as number }),
    ...(value.authentication_provider_id === undefined ? {} : { authenticationProviderID: value.authentication_provider_id as string }),
  };
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
export function boundedText(value: unknown, maximum: number): value is string {
  return typeof value === "string" && value.trim() !== "" && value.length <= maximum;
}
function nonnegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}
function positiveTimestamp(value: unknown): value is number {
  return nonnegativeInteger(value) && value > 0 && value <= 8.64e15;
}
