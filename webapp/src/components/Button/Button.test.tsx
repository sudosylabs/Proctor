import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { Button, ButtonLink } from "./Button";

describe("Button", () => {
  it("owns the pending label and disabled state", () => {
    const markup = renderToStaticMarkup(
      <Button isLoading loadingLabel="Saving…" type="submit">
        Save changes
      </Button>,
    );

    expect(markup).toContain('type="submit"');
    expect(markup).toContain('aria-busy="true"');
    expect(markup).toContain("disabled");
    expect(markup).toContain("Saving…");
    expect(markup).not.toContain("Save changes");
  });

  it("keeps navigation as an anchor", () => {
    const markup = renderToStaticMarkup(
      <ButtonLink href="/login">Sign in</ButtonLink>,
    );

    expect(markup).toContain('<a href="/login"');
    expect(markup).not.toContain("<button");
  });
});
