import { isValidElement, type ReactElement } from "react";
import { describe, expect, it } from "vitest";

import { DesktopAuthorizationPage } from "../features/desktop-authorization/DesktopAuthorizationPage";
import { JoinPage } from "../features/join/JoinPage";
import { ResetPasswordPage } from "../features/reset-password/ResetPasswordPage";
import { VerifyEmailPage } from "../features/verify-email/VerifyEmailPage";
import {
  authoredHostedRoutes,
  bootstrapHostedPage,
  hostedRouteDocumentTitle,
  renderHostedPage,
  type HostedPageBootstrap,
} from "./HostedRoutes";
import { hostedRoutes } from "./routes";

function bootstrap(path: string, hash = "") {
  const replacements: string[] = [];
  const value = bootstrapHostedPage(
    {
      href: `https://proctor.example${path}${hash}`,
      hash,
      pathname: path.split("?", 1)[0] ?? path,
    },
    {
      state: null,
      replaceState: (_state, _unused, url) => replacements.push(String(url)),
    },
  );
  return { replacements, value };
}

describe("hosted route descriptors", () => {
  it("is exhaustive against the generated server route catalog", () => {
    expect(authoredHostedRoutes).toEqual(hostedRoutes);
  });

  it("assigns one localized document purpose to every hosted route", () => {
    expect(
      Object.fromEntries(
        hostedRoutes.map((route) => [
          route,
          hostedRouteDocumentTitle(route),
        ]),
      ),
    ).toEqual({
      "/setup": "webapp.setup.document_title",
      "/login": "webapp.login.document_title",
      "/register": "webapp.register.document_title",
      "/authorize/desktop": "webapp.desktop_authorization.document_title",
      "/join": "webapp.join.document_title",
      "/account/forgot-password": "webapp.forgot_password.document_title",
      "/account/reset-password": "webapp.reset_password.document_title",
      "/account/verify-email": "webapp.verify_email.document_title",
      "/account/connect-provider": "webapp.connect_provider.document_title",
      "/authorization/complete":
        "webapp.authorization_complete.document_title",
    });
  });

  it("renders every route and no unknown fallback", () => {
    const bootstraps: HostedPageBootstrap[] = hostedRoutes.map(
      (route) => ({ route }) as HostedPageBootstrap,
    );
    for (const value of bootstraps) {
      expect(isValidElement(renderHostedPage(value))).toBe(true);
    }
    expect(bootstrap("/").value).toBeUndefined();
  });
});

describe("hosted route bootstrap", () => {
  it("sanitizes unexpected fragments on every credential-free route", () => {
    for (const route of [
      "/setup",
      "/register",
      "/account/forgot-password",
      "/account/connect-provider",
      "/authorization/complete",
    ] as const) {
      const result = bootstrap(route, "#unexpected=secret");
      expect(result.replacements).toEqual([
        `https://proctor.example${route}`,
      ]);
      expect(result.value).toEqual({ route });
    }
  });

  it("captures only the exact login notice and sanitizes everything else", () => {
    expect(bootstrap("/login", "#external_login=failed")).toEqual({
      replacements: ["https://proctor.example/login"],
      value: { route: "/login", notice: "external_login_failed" },
    });
    expect(bootstrap("/login", "#token=secret")).toEqual({
      replacements: ["https://proctor.example/login"],
      value: { route: "/login" },
    });
  });

  it.each([
    ["/join", "invitation_claim"],
    ["/account/reset-password", "password_reset_token"],
    ["/account/verify-email", "email_verification_token"],
  ] as const)("purpose-types and sanitizes %s tokens", (route, kind) => {
    const result = bootstrap(route, "#token=secret");
    expect(result.replacements).toEqual([`https://proctor.example${route}`]);
    expect(result.value).toEqual({
      route,
      credential: { kind, value: "secret" },
    });
  });

  it.each([
    "/join",
    "/account/reset-password",
    "/account/verify-email",
  ] as const)("rejects and removes a wrong credential on %s", (route) => {
    const result = bootstrap(route, "#proof=secret");
    expect(result.replacements).toEqual([`https://proctor.example${route}`]);
    expect(result.value).toEqual({ route });
  });

  it("captures the Desktop proof and bounds its query evidence", () => {
    expect(
      bootstrap(
        "/authorize/desktop?request=handle&state=state",
        "#proof=secret",
      ),
    ).toEqual({
      replacements: [
        "https://proctor.example/authorize/desktop?state=state",
      ],
      value: {
        route: "/authorize/desktop",
        state: "state",
        credential: {
          kind: "desktop_browser_proof",
          handle: "handle",
          value: "secret",
        },
      },
    });

    const oversizedState = "s".repeat(129);
    expect(
      bootstrap(
        `/authorize/desktop?request=handle&state=${oversizedState}`,
        "#proof=secret",
      ).value,
    ).toEqual({
      route: "/authorize/desktop",
    });

    expect(bootstrap("/authorize/desktop?state=state")).toEqual({
      replacements: [],
      value: { route: "/authorize/desktop", state: "state" },
    });

    expect(bootstrap("/authorize/desktop", "#token=secret")).toEqual({
      replacements: ["https://proctor.example/authorize/desktop"],
      value: { route: "/authorize/desktop" },
    });
  });
});

describe("purpose-specific page projection", () => {
  it("passes each one-time credential only to its owning page", () => {
    const join = renderHostedPage({
      route: "/join",
      credential: { kind: "invitation_claim", value: "claim" },
    }) as ReactElement<{ claim?: string }>;
    expect(join.type).toBe(JoinPage);
    expect(join.props.claim).toBe("claim");

    const reset = renderHostedPage({
      route: "/account/reset-password",
      credential: { kind: "password_reset_token", value: "reset" },
    }) as ReactElement<{ token?: string }>;
    expect(reset.type).toBe(ResetPasswordPage);
    expect(reset.props.token).toBe("reset");

    const verify = renderHostedPage({
      route: "/account/verify-email",
      credential: { kind: "email_verification_token", value: "verify" },
    }) as ReactElement<{ token?: string }>;
    expect(verify.type).toBe(VerifyEmailPage);
    expect(verify.props.token).toBe("verify");

    const desktop = renderHostedPage({
      route: "/authorize/desktop",
      credential: {
        kind: "desktop_browser_proof",
        handle: "handle",
        value: "proof",
      },
      state: "state",
    }) as ReactElement<{ proof?: { browserProof: string } }>;
    expect(desktop.type).toBe(DesktopAuthorizationPage);
    expect(desktop.props.proof?.browserProof).toBe("proof");
  });
});
