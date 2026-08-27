import { useEffect, useRef, useState } from "react";

import type { EmailVerificationResult } from "../../features/verify-email/VerifyEmailApi";
import { message } from "../../i18n/messages";
import { Button } from "../Button/Button";
import { Notice, type NoticeTone } from "../Notice/Notice";
import styles from "./EmailVerificationContent.module.css";

type VerificationState =
  | "ready"
  | "verifying"
  | "verified"
  | "invalid"
  | "unavailable";

export interface EmailVerificationContentProps {
  token?: string;
  verifyEmail(token: string): Promise<EmailVerificationResult>;
}

export function EmailVerificationContent({
  token,
  verifyEmail,
}: EmailVerificationContentProps) {
  const tokenRef = useRef(token);
  const activeRef = useRef(true);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [state, setState] = useState<VerificationState>(
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
    if (state === "verifying" || currentToken === undefined) {
      return;
    }

    setState("verifying");
    setLiveMessage(message("webapp.verify_email.verifying.body"));
    const result = await verifyEmail(currentToken);
    if (!activeRef.current) {
      return;
    }

    const nextState = result.kind;
    if (nextState === "verified" || nextState === "invalid") {
      tokenRef.current = undefined;
    }
    setState(nextState);
    setLiveMessage(copyFor(nextState).body);
    requestAnimationFrame(() => headingRef.current?.focus({ preventScroll: true }));
  }

  const copy = copyFor(state);

  return (
    <section
      className={`${styles.status} ${styles[state]}`}
      aria-labelledby="verify-email-heading"
    >
      <div className={styles.visuallyHidden} aria-live="polite" aria-atomic="true">
        {liveMessage}
      </div>
      <p className={styles.label}>{copy.label}</p>
      <h1 id="verify-email-heading" ref={headingRef} tabIndex={-1}>
        {copy.heading}
      </h1>
      <p className={styles.body}>{copy.body}</p>
      <Notice className={styles.notice} tone={noticeToneFor(state)}>
        <p>{copy.notice}</p>
      </Notice>
      <VerificationActions state={state} onSubmit={submit} />
    </section>
  );
}

function VerificationActions({
  onSubmit,
  state,
}: {
  onSubmit(): void;
  state: VerificationState;
}) {
  if (state === "ready" || state === "verifying") {
    return (
      <div className={styles.actions}>
        <Button
          isLoading={state === "verifying"}
          loadingLabel={message("webapp.verify_email.verifying.action")}
          onClick={onSubmit}
        >
          {message("webapp.verify_email.ready.action")}
        </Button>
        <SignInLink />
      </div>
    );
  }
  if (state === "unavailable") {
    return (
      <div className={styles.actions}>
        <Button onClick={onSubmit}>
          {message("webapp.verify_email.unavailable.retry")}
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
      {message("webapp.verify_email.sign_in")}
    </a>
  );
}

function noticeToneFor(state: VerificationState): NoticeTone {
  switch (state) {
    case "ready":
    case "verifying":
      return "accent";
    case "verified":
      return "success";
    case "invalid":
      return "danger";
    case "unavailable":
      return "warning";
  }
}

function copyFor(state: VerificationState) {
  switch (state) {
    case "ready":
      return {
        label: message("webapp.verify_email.ready.label"),
        heading: message("webapp.verify_email.ready.heading"),
        body: message("webapp.verify_email.ready.body"),
        notice: message("webapp.verify_email.ready.notice"),
      };
    case "verifying":
      return {
        label: message("webapp.verify_email.verifying.label"),
        heading: message("webapp.verify_email.verifying.heading"),
        body: message("webapp.verify_email.verifying.body"),
        notice: message("webapp.verify_email.verifying.notice"),
      };
    case "verified":
      return {
        label: message("webapp.verify_email.verified.label"),
        heading: message("webapp.verify_email.verified.heading"),
        body: message("webapp.verify_email.verified.body"),
        notice: message("webapp.verify_email.verified.notice"),
      };
    case "invalid":
      return {
        label: message("webapp.verify_email.invalid.label"),
        heading: message("webapp.verify_email.invalid.heading"),
        body: message("webapp.verify_email.invalid.body"),
        notice: message("webapp.verify_email.invalid.notice"),
      };
    case "unavailable":
      return {
        label: message("webapp.verify_email.unavailable.label"),
        heading: message("webapp.verify_email.unavailable.heading"),
        body: message("webapp.verify_email.unavailable.body"),
        notice: message("webapp.verify_email.unavailable.notice"),
      };
  }
}
