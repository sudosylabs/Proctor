import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { FormFeedback } from "./FormFeedback";

describe("FormFeedback", () => {
  it("keeps an empty polite status region mounted before a result exists", () => {
    const markup = renderToStaticMarkup(<FormFeedback />);

    expect(markup).toContain('data-proctor-form-feedback=""');
    expect(markup).toContain('role="status"');
    expect(markup).toContain('aria-live="polite"');
    expect(markup).toContain('aria-atomic="true"');
    expect(markup).not.toContain("data-proctor-notice");
  });

  it("presents a failure with the shared danger evidence treatment", () => {
    const markup = renderToStaticMarkup(
      <FormFeedback message="Review the details and try again." />,
    );

    expect(markup).toContain("Review the details and try again.");
    expect(markup).toContain('data-proctor-notice-tone="danger"');
  });
});
