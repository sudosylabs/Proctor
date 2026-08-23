export type FragmentCredentialName = "token" | "proof";

export interface BrowserLocation {
  href: string;
  hash: string;
}

export interface BrowserHistory {
  state: unknown;
  replaceState(data: unknown, unused: string, url?: string | URL | null): void;
}

export function captureFragmentCredential(
  location: BrowserLocation,
  history: BrowserHistory,
  name: FragmentCredentialName,
): string | undefined {
  const fragment = location.hash.startsWith("#") ? location.hash.slice(1) : location.hash;
  if (fragment === "") {
    return undefined;
  }

  // Remove the complete fragment before interpreting its contents so a
  // malformed credential cannot remain in history, screenshots, or referrers.
  const sanitized = new URL(location.href);
  sanitized.hash = "";
  history.replaceState(history.state, "", sanitized);

  const parameters = new URLSearchParams(fragment);
  const value = parameters.get(name);
  if (value === null || value === "" || parameters.size !== 1) {
    return undefined;
  }
  return value;
}
