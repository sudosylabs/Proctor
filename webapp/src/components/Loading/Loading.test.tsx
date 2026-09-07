import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { Loading } from "./Loading";

describe("Loading", () => {
  it("provides a polite status and an accessible label for a decorative spinner", () => {
    const markup = renderToStaticMarkup(<Loading label="Loading options…" />);
    expect(markup).toContain('role="status"');
    expect(markup).toContain('aria-live="polite"');
    expect(markup).toContain('aria-atomic="true"');
    expect(markup).toContain('data-proctor-icon="loading"');
    expect(markup).toContain('aria-hidden="true"');
    expect(markup).toContain("Loading options…");
    expect(markup).not.toContain("tabindex");
  });

  it("leaves announcements to an owning button or existing live region", () => {
    const markup = renderToStaticMarkup(<Loading label="Checking your request…" showLabel announce={false} />);
    expect(markup).not.toContain("aria-live");
    expect(markup).not.toContain('role="status"');
    expect(markup).toContain("Checking your request…");
  });
});
