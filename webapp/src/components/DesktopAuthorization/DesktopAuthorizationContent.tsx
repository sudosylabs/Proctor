import { useState } from "react";

import type {
  DesktopApprovalResult,
  DesktopAuthorizationProof,
  DesktopCancellationResult,
  DesktopContextResult,
} from "../../features/desktop-authorization/DesktopAuthorizationApi";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { Button, ButtonLink } from "../Button/Button";
import { FormFeedback } from "../FormFeedback/FormFeedback";
import { Notice } from "../Notice/Notice";
import {
  TaskState,
  TaskStateActions,
  TaskStateAnnouncement,
} from "../TaskState/TaskState";
import styles from "./DesktopAuthorization.module.css";

type TerminalState = "ready" | "approved" | "cancelled" | "invalid";

export interface DesktopAuthorizationContentProps {
  approve(proof: DesktopAuthorizationProof): Promise<DesktopApprovalResult>;
  cancel(
    proof: DesktopAuthorizationProof,
  ): Promise<DesktopCancellationResult>;
  checking: boolean;
  context: DesktopContextResult;
  onApproved(redirectURL: string): void;
  onRetryContext(): void;
  proof?: DesktopAuthorizationProof;
}

export function DesktopAuthorizationContent({
  approve,
  cancel,
  checking,
  context,
  onApproved,
  onRetryContext,
  proof,
}: DesktopAuthorizationContentProps) {
  const [terminal, setTerminal] = useState<TerminalState>(
    proof === undefined ? "invalid" : "ready",
  );
  const [pending, setPending] = useState<"approve" | "cancel">();
  const [feedback, setFeedback] = useState<string>();
  const [retryRequested, setRetryRequested] = useState(false);
  const [transitionTriggered, setTransitionTriggered] = useState(false);

  async function approveRequest() {
    if (proof === undefined || pending !== undefined) {
      return;
    }
    setPending("approve");
    setFeedback(undefined);
    const result = await approve(proof);
    if (result.kind === "approved") {
      setTransitionTriggered(true);
      setTerminal("approved");
      onApproved(result.redirectURL);
      return;
    }
    if (result.kind === "invalid") {
      setTransitionTriggered(true);
      setTerminal("invalid");
    } else if (result.kind === "no_session") {
      setFeedback(message("webapp.desktop_authorization.error.session"));
    } else {
      setFeedback(message("webapp.desktop_authorization.error.unavailable"));
    }
    setPending(undefined);
  }

  async function cancelRequest() {
    if (proof === undefined || pending !== undefined) {
      return;
    }
    setPending("cancel");
    setFeedback(undefined);
    const result = await cancel(proof);
    if (result.kind === "cancelled") {
      setTransitionTriggered(true);
      setTerminal("cancelled");
    } else if (result.kind === "invalid") {
      setTransitionTriggered(true);
      setTerminal("invalid");
    } else {
      setFeedback(message("webapp.desktop_authorization.error.unavailable"));
    }
    setPending(undefined);
  }

  function retryContext() {
    setRetryRequested(true);
    onRetryContext();
  }

  if (terminal !== "ready") {
    const heading = terminalCopy(terminal).heading;
    return (
      <>
        <TaskStateAnnouncement
          message={transitionTriggered ? heading : ""}
        />
        <TerminalContent
          focusHeading={transitionTriggered}
          state={terminal}
        />
      </>
    );
  }
  if (checking) {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.desktop_authorization.checking.heading")}
        />
        <TaskState
          body={message("webapp.desktop_authorization.checking.body")}
          busy
          className={styles.taskState}
          heading={message("webapp.desktop_authorization.checking.heading")}
          headingID="desktop-heading"
          label={message("webapp.desktop_authorization.label")}
        />
      </>
    );
  }
  if (context.kind === "no_session") {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.desktop_authorization.no_session.heading")}
        />
        <TaskState
          body={message("webapp.desktop_authorization.no_session.body")}
          className={styles.taskState}
          focusHeading={retryRequested}
          heading={message("webapp.desktop_authorization.no_session.heading")}
          headingID="desktop-heading"
          label={message("webapp.desktop_authorization.label")}
        >
          <TaskStateActions>
            <ButtonLink href="/login" target="_blank">
              {message("webapp.desktop_authorization.no_session.sign_in")}
            </ButtonLink>
            <Button variant="text" onClick={retryContext}>
              {message("webapp.desktop_authorization.no_session.retry")}
            </Button>
          </TaskStateActions>
        </TaskState>
      </>
    );
  }
  if (context.kind === "unavailable") {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.desktop_authorization.unavailable.heading")}
        />
        <TaskState
          body={message("webapp.desktop_authorization.unavailable.body")}
          className={styles.taskState}
          focusHeading={retryRequested}
          heading={message("webapp.desktop_authorization.unavailable.heading")}
          headingID="desktop-heading"
          label={message("webapp.desktop_authorization.label")}
        >
          <TaskStateActions>
            <Button onClick={retryContext}>
              {message("webapp.desktop_authorization.unavailable.retry")}
            </Button>
          </TaskStateActions>
        </TaskState>
      </>
    );
  }

  return (
    <>
      <TaskStateAnnouncement
        message={message("webapp.desktop_authorization.heading")}
      />
      <section className={styles.page} aria-labelledby="desktop-heading">
        <AccessTaskIntro
          eyebrow={message("webapp.desktop_authorization.label")}
          focusHeading={retryRequested}
          heading={message("webapp.desktop_authorization.heading")}
          body={message("webapp.desktop_authorization.lede")}
          headingID="desktop-heading"
        />
        <div className={styles.details}>
          <h2>{message("webapp.desktop_authorization.details")}</h2>
          <dl>
            <div>
              <dt>{message("webapp.desktop_authorization.installation")}</dt>
              <dd>{context.context.installation}</dd>
            </div>
            <div>
              <dt>{message("webapp.desktop_authorization.account")}</dt>
              <dd>{context.context.account}</dd>
            </div>
            <div>
              <dt>{message("webapp.desktop_authorization.request")}</dt>
              <dd>{message("webapp.desktop_authorization.request_value")}</dd>
            </div>
          </dl>
        </div>
        <Notice role="note" tone="warning">
          {message("webapp.desktop_authorization.caution")}
        </Notice>
        <TaskStateActions>
          <Button
            isLoading={pending === "approve"}
            loadingLabel={message("webapp.desktop_authorization.approving")}
            onClick={approveRequest}
          >
            {message("webapp.desktop_authorization.approve")}
          </Button>
          <Button
            disabled={pending === "approve"}
            isLoading={pending === "cancel"}
            loadingLabel={message("webapp.desktop_authorization.cancelling")}
            variant="text"
            onClick={cancelRequest}
          >
            {message("webapp.desktop_authorization.cancel")}
          </Button>
        </TaskStateActions>
        <p className={styles.expiry}>
          {message("webapp.desktop_authorization.expiry")}
        </p>
        <FormFeedback message={feedback} />
      </section>
    </>
  );
}

function TerminalContent({
  focusHeading,
  state,
}: {
  focusHeading: boolean;
  state: Exclude<TerminalState, "ready">;
}) {
  const content = terminalCopy(state);
  return (
    <TaskState
      body={content.body}
      className={styles.taskState}
      focusHeading={focusHeading}
      heading={content.heading}
      headingID="desktop-heading"
      label={content.label}
    >
      {state === "invalid" ? (
        <TaskStateActions>
          <ButtonLink href="/login" variant="secondary">
            {message("webapp.desktop_authorization.return_to_sign_in")}
          </ButtonLink>
        </TaskStateActions>
      ) : null}
    </TaskState>
  );
}

function terminalCopy(state: Exclude<TerminalState, "ready">) {
  switch (state) {
    case "approved":
      return {
        label: message("webapp.desktop_authorization.approved.label"),
        heading: message("webapp.desktop_authorization.approved.heading"),
        body: message("webapp.desktop_authorization.approved.body"),
      };
    case "cancelled":
      return {
        label: message("webapp.desktop_authorization.cancelled.label"),
        heading: message("webapp.desktop_authorization.cancelled.heading"),
        body: message("webapp.desktop_authorization.cancelled.body"),
      };
    case "invalid":
      return {
        label: message("webapp.desktop_authorization.invalid.label"),
        heading: message("webapp.desktop_authorization.invalid.heading"),
        body: message("webapp.desktop_authorization.invalid.body"),
      };
  }
}
