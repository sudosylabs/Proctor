import { describe, expect, it } from "vitest";

import { csrfHeaderName, readCookie, withCSRFHeader } from "./csrf";

describe("browser CSRF handling", () => {
  it("copies the readable cookie onto unsafe requests", () => {
    const headers = withCSRFHeader("POST", "other=x; PROCTOR_CSRF=proof%2Evalue");
    expect(headers.get(csrfHeaderName)).toBe("proof.value");
  });

  it("does not add the header to safe requests or replace an explicit value", () => {
    expect(withCSRFHeader("GET", "PROCTOR_CSRF=proof").has(csrfHeaderName)).toBe(false);
    expect(withCSRFHeader("DELETE", "PROCTOR_CSRF=proof", { [csrfHeaderName]: "explicit" }).get(csrfHeaderName)).toBe("explicit");
  });

  it("rejects malformed cookie encoding", () => {
    expect(readCookie("PROCTOR_CSRF=%", "PROCTOR_CSRF")).toBeUndefined();
  });
});
