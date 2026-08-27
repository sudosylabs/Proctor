import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { Icon, type IconName, type IconSize } from "./Icon";

const names: IconName[] = ["showPassword", "hidePassword"];

describe("Icon", () => {
  it.each(names)("renders the governed %s icon as decorative SVG", (name) => {
    const markup = renderToStaticMarkup(<Icon name={name} />);

    expect(markup).toContain("<svg");
    expect(markup).toContain('aria-hidden="true"');
    expect(markup).toContain('focusable="false"');
    expect(markup).toContain('stroke="currentColor"');
    expect(markup).toContain(`data-proctor-icon="${name}"`);
    expect(markup).not.toContain("aria-label");
  });

  it.each(["small", "default", "large"] satisfies IconSize[])(
    "accepts the governed %s size",
    (size) => {
      expect(renderToStaticMarkup(<Icon name="showPassword" size={size} />)).toContain(
        "<svg",
      );
    },
  );
});
