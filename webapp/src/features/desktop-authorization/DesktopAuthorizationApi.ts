import type { components } from "../../api/generated/schema";
import { apiClient } from "../../api/client";
import { readProblemValue } from "../../api/problem";
import {
  requestPublicAccessDiscovery,
  type PublicAccessDiscoveryLoader,
} from "../../auth/PublicAccessDiscovery";

type ServerContext = components["schemas"]["DesktopAuthorizationContextResponse"];
export type DesktopAuthorizationProvider =
  components["schemas"]["ExternalAuthenticationProviderResponse"];

export interface DesktopAuthorizationContext {
  account?: components["schemas"]["DesktopAuthorizationAccountResponse"];
  deviceName: string;
  expiresAt: number;
  externalProviders: DesktopAuthorizationProvider[];
  installation: string;
  localLoginEnabled: boolean;
  state: "bound" | "authenticated";
}

export type DesktopContextResult =
  | { kind: "ready"; context: DesktopAuthorizationContext }
  | { kind: "invalid" }
  | { kind: "locked" }
  | { kind: "unavailable" };

export interface DesktopAuthorizationProof {
  browserProof: string;
  handle: string;
  state: string;
}

export interface DesktopLocalAuthenticationSubmission {
  loginID: string;
  mfaCode?: string;
  password: string;
}

export type DesktopAuthenticationResult =
  | { kind: "authenticated"; context: DesktopAuthorizationContext }
  | { kind: "no_session" }
  | { kind: "mfa_required" }
  | { kind: "mfa_invalid" }
  | { kind: "invalid_credentials" }
  | { kind: "rate_limited" }
  | { kind: "invalid" }
  | { kind: "locked" }
  | { kind: "unavailable" };

export type DesktopApprovalResult =
  | { kind: "approved"; redirectURL: string }
  | { kind: "invalid" }
  | { kind: "locked" }
  | { kind: "unavailable" };

export type DesktopCancellationResult =
  | { kind: "cancelled" }
  | { kind: "invalid" }
  | { kind: "locked" }
  | { kind: "unavailable" };

export type DesktopBindingResult =
  | { kind: "bound" }
  | Exclude<DesktopContextResult, { kind: "ready" }>;

export type DesktopAccountResetResult =
  | { kind: "reset" }
  | Exclude<DesktopContextResult, { kind: "ready" }>;

export async function bindDesktopAuthorization(
  proof: DesktopAuthorizationProof,
): Promise<DesktopBindingResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/bind",
      {
        body: {
          handle: proof.handle,
          browser_proof: proof.browserProof,
          state: proof.state,
        },
      },
    );
    return response.status === 204 ? { kind: "bound" } : desktopFailure(error);
  } catch {
    return { kind: "unavailable" };
  }
}

export async function requestDesktopAuthorizationContext(
  servingOrigin: string,
  loadDiscovery: PublicAccessDiscoveryLoader = requestPublicAccessDiscovery,
): Promise<DesktopContextResult> {
  try {
    const [contextResult, discoveryResult] = await Promise.all([
      apiClient.GET("/api/v1/auth/desktop/authorizations/context"),
      loadDiscovery(servingOrigin),
    ]);
    if (contextResult.response.status !== 200) {
      return desktopFailure(contextResult.error);
    }
    if (
      discoveryResult.kind !== "ready" ||
      discoveryResult.discovery.institution?.display_name === undefined ||
      discoveryResult.discovery.institution.display_name === "" ||
      !validServerContext(contextResult.data)
    ) {
      return { kind: "unavailable" };
    }
    return {
      kind: "ready",
      context: projectContext(
        contextResult.data,
        discoveryResult.discovery.institution.display_name,
      ),
    };
  } catch {
    return { kind: "unavailable" };
  }
}

export async function authenticateDesktopAuthorizationSession(
  installation: string,
): Promise<DesktopAuthenticationResult> {
  try {
    const { data, error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/authenticate/session",
    );
    if (response.status === 200 && validServerContext(data) && data.state === "authenticated") {
      return {
        kind: "authenticated",
        context: projectContext(data, installation),
      };
    }
    const code = readProblemValue(error)?.code;
    if (
      code === "authentication.required" ||
      code === "authentication.invalid_token"
    ) {
      return { kind: "no_session" };
    }
    return desktopAuthenticationFailure(code);
  } catch {
    return { kind: "unavailable" };
  }
}

export async function authenticateDesktopAuthorizationLocally(
  installation: string,
  submission: DesktopLocalAuthenticationSubmission,
): Promise<DesktopAuthenticationResult> {
  try {
    const { data, error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/authenticate/password",
      {
        body: {
          login_id: submission.loginID,
          password: submission.password,
          ...(submission.mfaCode === undefined
            ? {}
            : { mfa_code: submission.mfaCode }),
        },
      },
    );
    if (response.status === 200 && validServerContext(data) && data.state === "authenticated") {
      return {
        kind: "authenticated",
        context: projectContext(data, installation),
      };
    }
    return desktopAuthenticationFailure(readProblemValue(error)?.code);
  } catch {
    return { kind: "unavailable" };
  }
}

export function desktopAuthorizationProviderURL(
  providerID: string,
  state: string,
): string {
  const parameters = new URLSearchParams({ state });
  return `/api/v1/auth/desktop/authorizations/authenticate/providers/${encodeURIComponent(providerID)}/login?${parameters.toString()}`;
}

export async function resetDesktopAuthorizationAccount(): Promise<DesktopAccountResetResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/account/reset",
    );
    return response.status === 204 ? { kind: "reset" } : desktopFailure(error);
  } catch {
    return { kind: "unavailable" };
  }
}

export async function approveDesktopAuthorization(
  state: string,
): Promise<DesktopApprovalResult> {
  try {
    const { data, error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/approve",
      { body: { state } },
    );
    if (response.status === 200 && isRecord(data) && positiveMilliseconds(data.expires_at) &&
      validApprovalRedirect(data.redirect_url, state)) {
      return { kind: "approved", redirectURL: data.redirect_url };
    }
    return desktopFailure(error);
  } catch {
    return { kind: "unavailable" };
  }
}

export async function cancelDesktopAuthorization(
  state: string,
): Promise<DesktopCancellationResult> {
  try {
    const { error, response } = await apiClient.POST(
      "/api/v1/auth/desktop/authorizations/cancel",
      { body: { state } },
    );
    return response.status === 204
      ? { kind: "cancelled" }
      : desktopFailure(error);
  } catch {
    return { kind: "unavailable" };
  }
}

function projectContext(
  value: ServerContext,
  installation: string,
): DesktopAuthorizationContext {
  return {
    state: value.state,
    installation,
    deviceName: value.device_name,
    expiresAt: value.expires_at,
    localLoginEnabled: value.local_login_enabled,
    externalProviders: value.external_providers,
    ...(value.account === undefined ? {} : { account: value.account }),
  };
}

function validServerContext(value: unknown): value is ServerContext {
  if (
    !isRecord(value) ||
    (value.state !== "bound" && value.state !== "authenticated")
  ) {
    return false;
  }
  if (
    !boundedString(value.device_name, 128) ||
    !positiveMilliseconds(value.expires_at) ||
    typeof value.local_login_enabled !== "boolean" ||
    !Array.isArray(value.external_providers) || value.external_providers.length > 64 ||
    !value.external_providers.every(validProvider) ||
    new Set(value.external_providers.map((provider) => provider.id)).size !== value.external_providers.length
  ) {
    return false;
  }
  if (value.state === "authenticated") {
    return (
      isRecord(value.account) &&
      boundedString(value.account.id, 64, true) &&
      boundedString(value.account.username, 128, true) &&
      boundedString(value.account.display_name, 256)
    );
  }
  return value.account === undefined;
}

function boundedString(value: unknown, maximum: number, required = false): value is string {
  return typeof value === "string" && (!required || value.trim() !== "") && [...value].length <= maximum;
}

function positiveMilliseconds(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function validProvider(value: unknown): value is DesktopAuthorizationProvider {
  return isRecord(value) && typeof value.id === "string" && /^[a-z0-9][a-z0-9._-]{0,63}$/.test(value.id) &&
    boundedString(value.display_name, 128, true) && boundedString(value.type, 64, true);
}

// The server owns the registered callback. The browser accepts only the same
// narrow loopback protocol and the exact state it is approving.
function validApprovalRedirect(value: unknown, state: string): value is string {
  if (typeof value !== "string" || value.length > 2048) return false;
  // The server preserves a valid decimal port, including leading zeroes.
  // Check the exact loopback spelling without relying on URL's port rewriting.
  const callbackBase = /^http:\/\/(?:127\.0\.0\.1|\[::1\]):[0-9]+\//.exec(value)?.[0];
  if (callbackBase === undefined) return false;
  try {
    const url = new URL(value);
    const path = url.pathname.slice(1);
    const code = url.searchParams.get("code");
    return url.protocol === "http:" && (url.hostname === "127.0.0.1" || url.hostname === "[::1]") &&
      Number(url.port) >= 49152 && Number(url.port) <= 65535 && url.username === "" && url.password === "" &&
      url.hash === "" && /^[A-Za-z0-9_-]{43}$/.test(path) && code !== null && /^[A-Za-z0-9_-]{43}$/.test(code) &&
      url.searchParams.getAll("code").length === 1 && url.searchParams.getAll("state").length === 1 &&
      url.searchParams.get("state") === state && [...url.searchParams.keys()].length === 2 &&
      value === `${callbackBase}${path}?${url.searchParams.toString()}`;
  } catch {
    return false;
  }
}

function desktopAuthenticationFailure(
  code: string | undefined,
): DesktopAuthenticationResult {
  switch (code) {
    case "authentication.mfa.required":
      return { kind: "mfa_required" };
    case "authentication.mfa.invalid_code":
      return { kind: "mfa_invalid" };
    case "authentication.invalid_credentials":
      return { kind: "invalid_credentials" };
    case "authentication.rate_limited":
      return { kind: "rate_limited" };
    default:
      return desktopFailureFromCode(code);
  }
}

function desktopFailure(
  error: unknown,
): Exclude<DesktopContextResult, { kind: "ready" }> {
  return desktopFailureFromCode(readProblemValue(error)?.code);
}

function desktopFailureFromCode(
  code: string | undefined,
): Exclude<DesktopContextResult, { kind: "ready" }> {
  if (code === "authentication.desktop_authorization.account_session_locked") {
    return { kind: "locked" };
  }
  if (
    code === "authentication.desktop_authorization.invalid" ||
    code === "authentication.desktop_authorization.rejected" ||
    code === "request.invalid"
  ) {
    return { kind: "invalid" };
  }
  return { kind: "unavailable" };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
