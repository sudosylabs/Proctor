import { useEffect, useRef, useState } from "react";

import type { EmailVerificationResult } from "../../features/verify-email/VerifyEmailApi";
import { message } from "../../i18n/messages";
import { Button, ButtonLink } from "../Button/Button";
import { Icon, type IconName } from "../Icon/Icon";
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
    requestAnimationFrame(() => headingRef.current?.focus());
  }

  const copy = copyFor(state);
  const iconName: IconName =
    state === "invalid" || state === "unavailable" ? "warning" : "mail";

  return (
    <section
      className={`${styles.status} ${styles[state]}`}
      aria-labelledby="verify-email-heading"
    >
      <div className={styles.visuallyHidden} aria-live="polite" aria-atomic="true">
        {liveMessage}
      </div>
      <div className={styles.iconFrame} aria-hidden="true">
        <Icon name={iconName} size="large" />
      </div>
      <div className={styles.content}>
        <p className={styles.label}>{copy.label}</p>
        <h1 id="verify-email-heading" ref={headingRef} tabIndex={-1}>
          {copy.heading}
        </h1>
        <p className={styles.body}>{copy.body}</p>
        <VerificationActions state={state} onSubmit={submit} />
      </div>
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
      </div>
    );
  }
  if (state === "unavailable") {
    return (
      <div className={styles.actions}>
        <Button onClick={onSubmit}>
          {message("webapp.verify_email.unavailable.retry")}
        </Button>
        <ButtonLink href="/login" variant="secondary">
          {message("webapp.verify_email.sign_in")}
        </ButtonLink>
      </div>
    );
  }
  return (
    <div className={styles.actions}>
      <ButtonLink href="/login">
        {message("webapp.verify_email.sign_in")}
      </ButtonLink>
    </div>
  );
}

function copyFor(state: VerificationState) {
  switch (state) {
    case "ready":
      return {
        label: message("webapp.verify_email.ready.label"),
        heading: message("webapp.verify_email.ready.heading"),
        body: message("webapp.verify_email.ready.body"),
      };
    case "verifying":
      return {
        label: message("webapp.verify_email.verifying.label"),
        heading: message("webapp.verify_email.verifying.heading"),
        body: message("webapp.verify_email.verifying.body"),
      };
    case "verified":
      return {
        label: message("webapp.verify_email.verified.label"),
        heading: message("webapp.verify_email.verified.heading"),
        body: message("webapp.verify_email.verified.body"),
      };
    case "invalid":
      return {
        label: message("webapp.verify_email.invalid.label"),
        heading: message("webapp.verify_email.invalid.heading"),
        body: message("webapp.verify_email.invalid.body"),
      };
    case "unavailable":
      return {
        label: message("webapp.verify_email.unavailable.label"),
        heading: message("webapp.verify_email.unavailable.heading"),
        body: message("webapp.verify_email.unavailable.body"),
      };
  }
}
