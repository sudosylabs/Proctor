import { describe, expect, it } from "vitest";

import { captureFragmentCredential } from "./fragments";

describe("fragment credential capture", () => {
  it("removes history before returning the credential", () => {
    const calls: string[] = [];
    const history = {
      state: { page: 1 },
      replaceState(_state: unknown, _unused: string, url?: string | URL | null) {
        calls.push(String(url));
      },
    };
    const token = captureFragmentCredential(
      { href: "https://proctor.example/join#token=secret", hash: "#token=secret" },
      history,
      "token",
    );
    expect(token).toBe("secret");
    expect(calls).toEqual(["https://proctor.example/join"]);
  });

  it("still removes malformed fragments", () => {
    const calls: string[] = [];
    const token = captureFragmentCredential(
      { href: "https://proctor.example/join#token=secret&other=value", hash: "#token=secret&other=value" },
      { state: null, replaceState: (_state, _unused, url) => calls.push(String(url)) },
      "token",
    );
    expect(token).toBeUndefined();
    expect(calls).toEqual(["https://proctor.example/join"]);
  });
});
