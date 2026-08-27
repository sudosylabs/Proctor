import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import {
  TaskState,
  TaskStateActions,
  TaskStateAnnouncement,
} from "./TaskState";

describe("TaskState", () => {
  it("keeps the task hierarchy and action grouping semantic", () => {
    const markup = renderToStaticMarkup(
      <TaskState
        headingID="task-heading"
        label="Account recovery"
        heading="Check your email"
        body="Use the message we sent."
      >
        <TaskStateActions>
          <a href="/login">Return to sign in</a>
        </TaskStateActions>
      </TaskState>,
    );
    expect(markup).toContain('aria-labelledby="task-heading"');
    expect(markup).toContain("<h1");
    expect(markup).toContain('id="task-heading"');
    expect(markup.indexOf("Account recovery")).toBeLessThan(
      markup.indexOf("Check your email"),
    );
    expect(markup.indexOf("Check your email")).toBeLessThan(
      markup.indexOf("Use the message we sent."),
    );
  });

  it("provides one persistent polite announcement path", () => {
    const markup = renderToStaticMarkup(
      <TaskStateAnnouncement message="The task is complete." />,
    );
    expect(markup).toContain('role="status"');
    expect(markup).toContain('aria-live="polite"');
    expect(markup).toContain('aria-atomic="true"');
  });

  it("exposes pending state and opt-in heading focus without changing hierarchy", () => {
    const markup = renderToStaticMarkup(
      <TaskState
        body="Please wait."
        busy
        focusHeading
        heading="Checking the request"
        headingID="checking-heading"
      />,
    );
    expect(markup).toContain('aria-busy="true"');
    expect(markup).toContain('id="checking-heading"');
    expect(markup).toContain('tabindex="-1"');
    expect(markup).not.toContain("<h2");
  });
});
