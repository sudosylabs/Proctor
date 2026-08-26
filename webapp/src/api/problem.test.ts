import { describe, expect, it } from "vitest";

import { readProblem, readProblemValue } from "./problem";

describe("readProblem", () => {
  it("returns a bounded Problem Details projection", async () => {
    const response = new Response(JSON.stringify({
      type: "https://proctor.example/problems/invalid",
      title: "Invalid request",
      status: 400,
      detail: "The request is invalid.",
      code: "request.invalid",
      private_value: "must not escape",
    }), { headers: { "Content-Type": "application/problem+json" } });

    await expect(readProblem(response)).resolves.toEqual({
      type: "https://proctor.example/problems/invalid",
      title: "Invalid request",
      status: 400,
      detail: "The request is invalid.",
      code: "request.invalid",
    });
  });

  it("ignores malformed and non-Problem responses", async () => {
    await expect(readProblem(new Response("not json", {
      headers: { "Content-Type": "application/problem+json" },
    }))).resolves.toBeUndefined();
    await expect(readProblem(new Response("{}", {
      headers: { "Content-Type": "application/json" },
    }))).resolves.toBeUndefined();
  });
});

describe("readProblemValue", () => {
  it("projects a parsed Problem without retaining private fields", () => {
    expect(readProblemValue({
      type: "/problems/authentication",
      title: "Authentication failed",
      status: 401,
      code: "authentication.invalid_credentials",
      private_value: "must not escape",
    })).toEqual({
      type: "/problems/authentication",
      title: "Authentication failed",
      status: 401,
      code: "authentication.invalid_credentials",
    });
  });
});
