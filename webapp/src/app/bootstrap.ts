import { isHostedRoute, type HostedRoute } from "./routes";
import {
  captureFragmentCredential,
  type BrowserHistory,
  type BrowserLocation,
  type FragmentCredentialName,
} from "../auth/fragments";

export type HostedPageCredential =
  | { kind: "invitation_claim"; value: string }
  | { kind: "desktop_browser_proof"; value: string }
  | { kind: "password_reset_token"; value: string }
  | { kind: "email_verification_token"; value: string };

export interface HostedPageBootstrap {
  route?: HostedRoute;
  credential?: HostedPageCredential;
  notice?: "external_login_failed";
}

type CredentialRoute = {
  name: FragmentCredentialName;
  kind: HostedPageCredential["kind"];
};

const credentialRoutes: Partial<Record<HostedRoute, CredentialRoute>> = {
  "/authorize/desktop": { name: "proof", kind: "desktop_browser_proof" },
  "/join": { name: "token", kind: "invitation_claim" },
  "/account/reset-password": { name: "token", kind: "password_reset_token" },
  "/account/verify-email": { name: "token", kind: "email_verification_token" },
};

export function bootstrapHostedPage(
  location: BrowserLocation & { pathname: string },
  history: BrowserHistory,
): HostedPageBootstrap {
  if (!isHostedRoute(location.pathname)) {
    return {};
  }
  const route = location.pathname;

  if (
    route === "/setup" ||
    route === "/login" ||
    route === "/register" ||
    route === "/authorization/complete"
  ) {
    const fragment = location.hash;
    if (fragment !== "") {
      const sanitized = new URL(location.href);
      sanitized.hash = "";
      history.replaceState(history.state, "", sanitized);
    }
    if (route === "/login" && fragment === "#external_login=failed") {
      return { route, notice: "external_login_failed" };
    }
    return { route };
  }

  const expected = credentialRoutes[route];
  if (expected === undefined) {
    return { route };
  }
  const value = captureFragmentCredential(location, history, expected.name);
  if (value === undefined) {
    return { route };
  }
  return { route, credential: { kind: expected.kind, value } };
}
