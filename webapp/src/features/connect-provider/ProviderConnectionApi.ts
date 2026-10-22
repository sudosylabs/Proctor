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
    const [userResult, providers] = await Promise.all([
      apiClient.GET("/api/v1/users/me"),
      requestAllProviders(),
    ]);
    if (userResult.response.status === 401) {
      return { kind: "no_session" };
    }
    if (
      userResult.response.status !== 200 ||
      providers === undefined ||
      !isRecord(userResult.data) ||
      typeof userResult.data.username !== "string"
    ) {
      return { kind: "unavailable" };
    }
    return {
      kind: "ready",
      context: {
        providers,
        username: userResult.data.username,
      },
    };
  } catch {
    return { kind: "unavailable" };
  }
}

// The provider collection is paged like every collection; the configured set
// is small, so the connection page reads every page up to a fixed bound.
const maximumProviderPages = 10;

async function requestAllProviders(): Promise<Provider[] | undefined> {
  const providers: Provider[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < maximumProviderPages; page += 1) {
    const result = await apiClient.GET("/api/v1/auth/providers", {
      params: { query: cursor === undefined ? {} : { cursor } },
    });
    if (
      result.response.status !== 200 ||
      !isRecord(result.data) ||
      !Array.isArray(result.data.items) ||
      !result.data.items.every(isProvider)
    ) {
      return undefined;
    }
    providers.push(...result.data.items);
    if (typeof result.data.next_cursor !== "string") {
      return providers;
    }
    cursor = result.data.next_cursor;
  }
  return undefined;
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
      code === "authentication.token_invalid" ||
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
