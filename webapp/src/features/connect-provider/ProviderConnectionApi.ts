import { apiClient } from "../../api/client";
import type { components } from "../../api/generated/schema";
import { readProblemValue } from "../../api/problem";

export type Provider =
  components["schemas"]["ExternalAuthenticationProviderResponse"];

export interface ProviderConnectionContext {
  providers: Provider[];
  username: string;
}

export type ProviderConnectionContextResult =
  | { kind: "ready"; context: ProviderConnectionContext }
  | { kind: "no_session" }
  | { kind: "unavailable" };

export type BeginProviderConnectionResult =
  | { kind: "redirect"; url: string }
  | { kind: "reauthentication_required" }
  | { kind: "unavailable" };

export async function requestProviderConnectionContext(): Promise<ProviderConnectionContextResult> {
  try {
    const [userResult, providerResult] = await Promise.all([
      apiClient.GET("/api/v1/users/me"),
      apiClient.GET("/api/v1/auth/providers"),
    ]);
    if (userResult.response.status === 401) {
      return { kind: "no_session" };
    }
    if (
      userResult.response.status !== 200 ||
      providerResult.response.status !== 200 ||
      !isRecord(userResult.data) ||
      typeof userResult.data.username !== "string" ||
      !Array.isArray(providerResult.data) ||
      !providerResult.data.every(isProvider)
    ) {
      return { kind: "unavailable" };
    }
    return {
      kind: "ready",
      context: {
        providers: providerResult.data,
        username: userResult.data.username,
      },
    };
  } catch {
    return { kind: "unavailable" };
  }
}

export async function beginProviderConnection(
  providerID: string,
): Promise<BeginProviderConnectionResult> {
  try {
    const { data, error, response } = await apiClient.POST(
      "/api/v1/authentication-methods/providers/{provider_id}/connect",
      {
        params: { path: { provider_id: providerID } },
        body: { return_to: "/authorization/complete" },
      },
    );
    if (
      response.status === 201 &&
      isRecord(data) &&
      typeof data.redirect_url === "string"
    ) {
      return { kind: "redirect", url: data.redirect_url };
    }
    const code = readProblemValue(error)?.code;
    if (
      code === "authentication.required" ||
      code === "authentication.invalid_token" ||
      code === "authentication.strong_required" ||
      code === "authentication.reauthentication_required"
    ) {
      return { kind: "reauthentication_required" };
    }
    return { kind: "unavailable" };
  } catch {
    return { kind: "unavailable" };
  }
}

function isProvider(value: unknown): value is Provider {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    value.id !== "" &&
    typeof value.display_name === "string" &&
    value.display_name !== "" &&
    typeof value.type === "string" &&
    value.type !== ""
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
