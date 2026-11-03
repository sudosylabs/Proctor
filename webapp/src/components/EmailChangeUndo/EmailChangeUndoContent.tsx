import { useEffect, useRef, useState } from "react";

import type { EmailChangeUndoResult } from "../../features/undo-email-change/UndoEmailChangeApi";
import { message, type MessageID } from "../../i18n/messages";
import { Button } from "../Button/Button";
import { Notice, type NoticeTone } from "../Notice/Notice";
import styles from "./EmailChangeUndoContent.module.css";

type UndoState =
  | "ready"
  | "undoing"
  | "restored"
  | "invalid"
  | "conflict"
  | "rate_limited"
  | "unavailable";

export interface EmailChangeUndoContentProps {
  token?: string;
  undoEmailChange(token: string): Promise<EmailChangeUndoResult>;
}

export function EmailChangeUndoContent({
  token,
  undoEmailChange,
}: EmailChangeUndoContentProps) {
  const tokenRef = useRef(token);
  const activeRef = useRef(true);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [state, setState] = useState<UndoState>(
    token === undefined ? "invalid" : "ready",
  );
  const [liveMessage, setLiveMessage] = useState("");

  useEffect(() => {
    activeRef.current = true;
    return () => {
      activeRef.current = false;
    };
  }, []);

  async function submit() {
    const currentToken = tokenRef.current;
    if (state === "undoing" || currentToken === undefined) {
      return;
    }

    setState("undoing");
    setLiveMessage(message("webapp.undo_email_change.undoing.body"));
    const result = await undoEmailChange(currentToken);
    if (!activeRef.current) {
      return;
    }

    const nextState = result.kind;
    if (nextState === "restored" || nextState === "invalid" || nextState === "conflict") {
      tokenRef.current = undefined;
    }
    setState(nextState);
    setLiveMessage(message(copyFor(nextState).body));
    requestAnimationFrame(() => headingRef.current?.focus({ preventScroll: true }));
  }

  const copy = copyFor(state);
  const visualState = visualStateFor(state);

  return (
    <section
      className={`${styles.status} ${styles[visualState]}`}
      aria-labelledby="undo-email-change-heading"
    >
      <div className={styles.visuallyHidden} aria-live="polite" aria-atomic="true">
        {liveMessage}
      </div>
      <p className={styles.label}>{message(copy.label)}</p>
      <h1 id="undo-email-change-heading" ref={headingRef} tabIndex={-1}>
        {message(copy.heading)}
      </h1>
      <p className={styles.body}>{message(copy.body)}</p>
      <Notice className={styles.notice} tone={noticeToneFor(state)}>
        <p>{message(copy.notice)}</p>
      </Notice>
      <UndoActions state={state} onSubmit={submit} />
    </section>
  );
}

function UndoActions({
  onSubmit,
  state,
}: {
  onSubmit(): void;
  state: UndoState;
}) {
  if (state === "ready" || state === "undoing") {
    return (
      <div className={styles.actions}>
        <Button
          isLoading={state === "undoing"}
          loadingLabel={message("webapp.undo_email_change.undoing.action")}
          onClick={onSubmit}
        >
          {message("webapp.undo_email_change.ready.action")}
        </Button>
        <SignInLink />
      </div>
    );
  }
  if (state === "unavailable" || state === "rate_limited") {
    return (
      <div className={styles.actions}>
        <Button onClick={onSubmit}>
          {message("webapp.undo_email_change.unavailable.retry")}
        </Button>
        <SignInLink />
      </div>
    );
  }
  return (
    <div className={styles.actions}>
      <SignInLink />
    </div>
  );
}

function SignInLink() {
  return (
    <a className={styles.signInLink} href="/login">
      {message("webapp.undo_email_change.sign_in")}
    </a>
  );
}

function visualStateFor(
  state: UndoState,
): "ready" | "restored" | "invalid" | "unavailable" {
  switch (state) {
    case "ready":
    case "undoing":
      return "ready";
    case "restored":
      return "restored";
    case "invalid":
    case "conflict":
      return "invalid";
    case "rate_limited":
    case "unavailable":
      return "unavailable";
  }
}

function noticeToneFor(state: UndoState): NoticeTone {
  switch (visualStateFor(state)) {
    case "ready":
      return "accent";
    case "restored":
      return "success";
    case "invalid":
      return "danger";
    case "unavailable":
      return "warning";
  }
}

interface UndoCopy {
  label: MessageID;
  heading: MessageID;
  body: MessageID;
  notice: MessageID;
}

function copyFor(state: UndoState): UndoCopy {
  switch (state) {
    case "ready":
      return {
        label: "webapp.undo_email_change.ready.label",
        heading: "webapp.undo_email_change.ready.heading",
        body: "webapp.undo_email_change.ready.body",
        notice: "webapp.undo_email_change.ready.notice",
      };
    case "undoing":
      return {
        label: "webapp.undo_email_change.undoing.label",
        heading: "webapp.undo_email_change.undoing.heading",
        body: "webapp.undo_email_change.undoing.body",
        notice: "webapp.undo_email_change.undoing.notice",
      };
    case "restored":
      return {
        label: "webapp.undo_email_change.restored.label",
        heading: "webapp.undo_email_change.restored.heading",
        body: "webapp.undo_email_change.restored.body",
        notice: "webapp.undo_email_change.restored.notice",
      };
    case "invalid":
      return {
        label: "webapp.undo_email_change.invalid.label",
        heading: "webapp.undo_email_change.invalid.heading",
        body: "webapp.undo_email_change.invalid.body",
        notice: "webapp.undo_email_change.invalid.notice",
      };
    case "conflict":
      return {
        label: "webapp.undo_email_change.conflict.label",
        heading: "webapp.undo_email_change.conflict.heading",
        body: "webapp.undo_email_change.conflict.body",
        notice: "webapp.undo_email_change.conflict.notice",
      };
    case "rate_limited":
      return {
        label: "webapp.undo_email_change.unavailable.label",
        heading: "webapp.undo_email_change.unavailable.heading",
        body: "webapp.undo_email_change.rate_limited.body",
        notice: "webapp.undo_email_change.unavailable.notice",
      };
    case "unavailable":
      return {
        label: "webapp.undo_email_change.unavailable.label",
        heading: "webapp.undo_email_change.unavailable.heading",
        body: "webapp.undo_email_change.unavailable.body",
        notice: "webapp.undo_email_change.unavailable.notice",
      };
  }
}
