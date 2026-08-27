import type { ReactNode } from "react";

import {
  captureFragmentCredential,
  type BrowserHistory,
  type BrowserLocation,
} from "../auth/fragments";
import { AuthorizationCompletePage } from "../features/authorization-complete/AuthorizationCompletePage";
import { ConnectProviderPage } from "../features/connect-provider/ConnectProviderPage";
import { DesktopAuthorizationPage } from "../features/desktop-authorization/DesktopAuthorizationPage";
import { ForgotPasswordPage } from "../features/forgot-password/ForgotPasswordPage";
import { JoinPage } from "../features/join/JoinPage";
import { LoginPage } from "../features/login/LoginPage";
import { RegisterPage } from "../features/register/RegisterPage";
import { ResetPasswordPage } from "../features/reset-password/ResetPasswordPage";
import { SetupPage } from "../features/setup/SetupPage";
import { VerifyEmailPage } from "../features/verify-email/VerifyEmailPage";
import type { MessageID } from "../i18n/messages";
import { isHostedRoute, type HostedRoute } from "./routes";

export type HostedPageBootstrap =
  | { route: "/setup" }
  | { route: "/login"; notice?: "external_login_failed" }
  | { route: "/register" }
  | {
      route: "/authorize/desktop";
      credential?: {
        kind: "desktop_browser_proof";
        handle?: string;
        state?: string;
        value: string;
      };
    }
  | {
      route: "/join";
      credential?: { kind: "invitation_claim"; value: string };
    }
  | { route: "/account/forgot-password" }
  | {
      route: "/account/reset-password";
      credential?: { kind: "password_reset_token"; value: string };
    }
  | {
      route: "/account/verify-email";
      credential?: { kind: "email_verification_token"; value: string };
    }
  | { route: "/account/connect-provider" }
  | { route: "/authorization/complete" };

type RouteBootstrap<R extends HostedRoute> = Extract<
  HostedPageBootstrap,
  { route: R }
>;

type FragmentPolicy =
  | { kind: "none" }
  | { kind: "external_login_notice" }
  | { kind: "invitation_claim"; name: "token" }
  | { kind: "desktop_browser_proof"; name: "proof" }
  | { kind: "password_reset_token"; name: "token" }
  | { kind: "email_verification_token"; name: "token" };

interface HostedRouteDescriptor<R extends HostedRoute> {
  documentTitle: MessageID;
  fragment: FragmentPolicy;
  render(bootstrap: RouteBootstrap<R>): ReactNode;
}

type HostedRouteDescriptorMap = {
  [R in HostedRoute]: HostedRouteDescriptor<R>;
};

const hostedRouteDescriptors = {
  "/setup": {
    documentTitle: "webapp.setup.document_title",
    fragment: { kind: "none" },
    render: () => <SetupPage />,
  },
  "/login": {
    documentTitle: "webapp.login.document_title",
    fragment: { kind: "external_login_notice" },
    render: (bootstrap) => (
      <LoginPage
        externalLoginFailed={bootstrap.notice === "external_login_failed"}
      />
    ),
  },
  "/register": {
    documentTitle: "webapp.register.document_title",
    fragment: { kind: "none" },
    render: () => <RegisterPage />,
  },
  "/authorize/desktop": {
    documentTitle: "webapp.desktop_authorization.document_title",
    fragment: { kind: "desktop_browser_proof", name: "proof" },
    render: (bootstrap) => (
      <DesktopAuthorizationPage
        proof={
          bootstrap.credential?.handle !== undefined &&
          bootstrap.credential.state !== undefined
            ? {
                browserProof: bootstrap.credential.value,
                handle: bootstrap.credential.handle,
                state: bootstrap.credential.state,
              }
            : undefined
        }
      />
    ),
  },
  "/join": {
    documentTitle: "webapp.join.document_title",
    fragment: { kind: "invitation_claim", name: "token" },
    render: (bootstrap) => (
      <JoinPage claim={bootstrap.credential?.value} />
    ),
  },
  "/account/forgot-password": {
    documentTitle: "webapp.forgot_password.document_title",
    fragment: { kind: "none" },
    render: () => <ForgotPasswordPage />,
  },
  "/account/reset-password": {
    documentTitle: "webapp.reset_password.document_title",
    fragment: { kind: "password_reset_token", name: "token" },
    render: (bootstrap) => (
      <ResetPasswordPage token={bootstrap.credential?.value} />
    ),
  },
  "/account/verify-email": {
    documentTitle: "webapp.verify_email.document_title",
    fragment: { kind: "email_verification_token", name: "token" },
    render: (bootstrap) => (
      <VerifyEmailPage token={bootstrap.credential?.value} />
    ),
  },
  "/account/connect-provider": {
    documentTitle: "webapp.connect_provider.document_title",
    fragment: { kind: "none" },
    render: () => <ConnectProviderPage />,
  },
  "/authorization/complete": {
    documentTitle: "webapp.authorization_complete.document_title",
    fragment: { kind: "none" },
    render: () => <AuthorizationCompletePage />,
  },
} satisfies HostedRouteDescriptorMap;

export function bootstrapHostedPage(
  location: BrowserLocation & { pathname: string },
  history: BrowserHistory,
): HostedPageBootstrap | undefined {
  if (!isHostedRoute(location.pathname)) {
    return undefined;
  }
  const route = location.pathname;
  const policy = hostedRouteDescriptors[route].fragment;

  switch (policy.kind) {
    case "none":
      clearFragment(location, history);
      return { route } as RouteBootstrap<typeof route>;
    case "external_login_notice": {
      const fragment = location.hash;
      clearFragment(location, history);
      return {
        route,
        ...(fragment === "#external_login=failed"
          ? { notice: "external_login_failed" as const }
          : {}),
      } as RouteBootstrap<typeof route>;
    }
    case "desktop_browser_proof": {
      const value = captureFragmentCredential(
        location,
        history,
        policy.name,
      );
      if (value === undefined) {
        return { route } as RouteBootstrap<typeof route>;
      }
      const url = new URL(location.href);
      const handle = boundedParameter(url.searchParams.get("request"));
      const state = boundedParameter(url.searchParams.get("state"));
      return {
        route,
        credential: {
          kind: "desktop_browser_proof",
          value,
          ...(handle === undefined ? {} : { handle }),
          ...(state === undefined ? {} : { state }),
        },
      } as RouteBootstrap<typeof route>;
    }
    case "invitation_claim":
    case "password_reset_token":
    case "email_verification_token": {
      const value = captureFragmentCredential(
        location,
        history,
        policy.name,
      );
      return {
        route,
        ...(value === undefined
          ? {}
          : { credential: { kind: policy.kind, value } }),
      } as RouteBootstrap<typeof route>;
    }
  }
}

export function hostedRouteDocumentTitle(route: HostedRoute): MessageID {
  return hostedRouteDescriptors[route].documentTitle;
}

export function renderHostedPage(bootstrap: HostedPageBootstrap): ReactNode {
  const descriptor = hostedRouteDescriptors[bootstrap.route];
  return descriptor.render(bootstrap as never);
}

export const authoredHostedRoutes = Object.freeze(
  Object.keys(hostedRouteDescriptors) as HostedRoute[],
);

function clearFragment(
  location: BrowserLocation,
  history: BrowserHistory,
): void {
  if (location.hash === "") {
    return;
  }
  const sanitized = new URL(location.href);
  sanitized.hash = "";
  history.replaceState(history.state, "", sanitized);
}

function boundedParameter(value: string | null): string | undefined {
  if (value === null || value === "" || value.length > 128) {
    return undefined;
  }
  return value;
}
