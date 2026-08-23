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

  it("does not interpret fragments on non-credential routes", () => {
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

    expect(replacements).toEqual([]);
    expect(bootstrap).toEqual({ route: "/login" });
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
