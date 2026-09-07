import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { Button, ButtonLink } from "./Button";

describe("Button", () => {
  it("keeps the action footprint while exposing a named pending spinner", () => {
    const markup = renderToStaticMarkup(
      <Button isLoading loadingLabel="Saving…" type="submit">
        Save changes
      </Button>,
    );

    expect(markup).toContain('type="submit"');
    expect(markup).toContain('aria-busy="true"');
    expect(markup).toContain("disabled");
    expect(markup).toContain("Saving…");
    expect(markup).toContain("Save changes");
    expect(markup).toMatch(/<span[^>]*aria-hidden="true"[^>]*>Save changes<\/span>/);
    expect(markup).toContain('data-proctor-icon="loading"');
    expect(markup).not.toContain('role="status"');
  });

  it("retains an accessible action name when no loading label is supplied", () => {
    const markup = renderToStaticMarkup(<Button isLoading>Retry</Button>);
    expect(markup).toContain('data-proctor-loading="true"');
    expect(markup.match(/Retry/g)).toHaveLength(2);
  });

  it("keeps navigation as an anchor", () => {
    const markup = renderToStaticMarkup(
      <ButtonLink href="/login">Sign in</ButtonLink>,
    );

    expect(markup).toContain('<a href="/login"');
    expect(markup).not.toContain("<button");
  });
});
