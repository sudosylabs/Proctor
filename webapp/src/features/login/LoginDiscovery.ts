import {
  requestPublicAccessDiscovery,
  type PublicAccessDiscovery,
  type PublicAccessDiscoveryResult,
} from "../../auth/PublicAccessDiscovery";

export type LoginDiscoveryState =
  | { kind: "loading" }
  | { kind: "ready"; discovery: PublicAccessDiscovery }
  | { kind: "setup"; discovery: PublicAccessDiscovery }
  | { kind: "unavailable"; discovery: PublicAccessDiscovery }
  | { kind: "origin_mismatch" }
  | { kind: "failure" };

export function resolveLoginDiscovery(
  result: PublicAccessDiscoveryResult,
): LoginDiscoveryState {
  if (result.kind === "unavailable") {
    return { kind: "failure" };
  }
  if (result.kind === "origin_mismatch") {
    return result;
  }
  const { discovery } = result;
  if (!discovery.initialized) {
    return { kind: "setup", discovery };
  }
  if (!discovery.capabilities.local_login && discovery.providers.length === 0) {
    return { kind: "unavailable", discovery };
  }
  return { kind: "ready", discovery };
}

export async function requestLoginDiscovery(
  servingOrigin: string,
): Promise<LoginDiscoveryState> {
  return resolveLoginDiscovery(
    await requestPublicAccessDiscovery(servingOrigin),
  );
}
