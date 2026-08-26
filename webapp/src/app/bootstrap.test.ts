import { describe, expect, it } from "vitest";

import { bootstrapHostedPage } from "./bootstrap";

describe("hosted page bootstrap", () => {
  it("sanitizes a join credential before exposing purpose-specific state", () => {
    const replacements: string[] = [];
    const bootstrap = bootstrapHostedPage(
      {
        href: "https://proctor.example/join#token=secret",
        hash: "#token=secret",
        pathname: "/join",
      },
      {
        state: null,
        replaceState: (_state, _unused, url) => replacements.push(String(url)),
      },
    );

    expect(replacements).toEqual(["https://proctor.example/join"]);
    expect(bootstrap).toEqual({
      route: "/join",
      credential: { kind: "invitation_claim", value: "secret" },
    });
  });

  it("removes unknown login fragments without interpreting them", () => {
    const replacements: string[] = [];
    const bootstrap = bootstrapHostedPage(
      {
        href: "https://proctor.example/login#token=secret",
        hash: "#token=secret",
        pathname: "/login",
      },
      {
        state: null,
        replaceState: (_state, _unused, url) => replacements.push(String(url)),
      },
    );

    expect(replacements).toEqual(["https://proctor.example/login"]);
    expect(bootstrap).toEqual({ route: "/login" });
  });

  it("captures only the bounded external-login failure notice", () => {
    const replacements: string[] = [];
    const bootstrap = bootstrapHostedPage(
      {
        href: "https://proctor.example/login#external_login=failed",
        hash: "#external_login=failed",
        pathname: "/login",
      },
      {
        state: null,
        replaceState: (_state, _unused, url) => replacements.push(String(url)),
      },
    );

    expect(replacements).toEqual(["https://proctor.example/login"]);
    expect(bootstrap).toEqual({
      route: "/login",
      notice: "external_login_failed",
    });
  });

  it("removes fragments from the Session-confirmation route", () => {
    const replacements: string[] = [];
    const bootstrap = bootstrapHostedPage(
      {
        href: "https://proctor.example/authorization/complete#unexpected",
        hash: "#unexpected",
        pathname: "/authorization/complete",
      },
      {
        state: null,
        replaceState: (_state, _unused, url) => replacements.push(String(url)),
      },
    );

    expect(replacements).toEqual([
      "https://proctor.example/authorization/complete",
    ]);
    expect(bootstrap).toEqual({ route: "/authorization/complete" });
  });

  it("removes unexpected fragments from setup and registration", () => {
    for (const route of ["/setup", "/register"] as const) {
      const replacements: string[] = [];
      const bootstrap = bootstrapHostedPage(
        {
          href: `https://proctor.example${route}#unexpected=secret`,
          hash: "#unexpected=secret",
          pathname: route,
        },
        {
          state: null,
          replaceState: (_state, _unused, url) => replacements.push(String(url)),
        },
      );

      expect(replacements).toEqual([`https://proctor.example${route}`]);
      expect(bootstrap).toEqual({ route });
    }
  });

  it("removes the desktop browser proof before rendering", () => {
    const replacements: string[] = [];
    const bootstrap = bootstrapHostedPage(
      {
        href: "https://proctor.example/authorize/desktop?request=handle&state=state#proof=secret",
        hash: "#proof=secret",
        pathname: "/authorize/desktop",
      },
      {
        state: null,
        replaceState: (_state, _unused, url) => replacements.push(String(url)),
      },
    );

    expect(replacements).toEqual([
      "https://proctor.example/authorize/desktop?request=handle&state=state",
    ]);
    expect(bootstrap).toEqual({
      route: "/authorize/desktop",
      credential: { kind: "desktop_browser_proof", value: "secret" },
    });
  });
});
