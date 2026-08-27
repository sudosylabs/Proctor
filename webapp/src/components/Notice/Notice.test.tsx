import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { Notice, type NoticeTone } from "./Notice";

describe("Notice", () => {
  it.each([
    "accent",
    "information",
    "success",
    "warning",
    "danger",
  ] satisfies NoticeTone[])("renders the governed %s tone", (tone) => {
    const markup = renderToStaticMarkup(
      <Notice tone={tone}>State evidence</Notice>,
    );

    expect(markup).toContain("State evidence");
    expect(markup).toContain(`data-proctor-notice-tone="${tone}"`);
  });

  it("forwards page-owned semantics, focus, and layout classes", () => {
    const markup = renderToStaticMarkup(
      <Notice
        aria-live="polite"
        className="local-layout"
        role="alert"
        tabIndex={-1}
      >
        Try again.
      </Notice>,
    );

    expect(markup).toContain('role="alert"');
    expect(markup).toContain('aria-live="polite"');
    expect(markup).toContain('tabindex="-1"');
    expect(markup).toContain("local-layout");
  });
});
