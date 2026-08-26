import type { ReactNode } from "react";

import type {
  SetupStatus,
  SetupSubmission,
  SetupSubmissionResult,
} from "../../features/setup/SetupApi";
import { message } from "../../i18n/messages";
import { Button, ButtonLink } from "../Button/Button";
import { SetupForm } from "./SetupForm";
import styles from "./Setup.module.css";

export interface SetupContentProps {
  onComplete(): void;
  onRetry(): void;
  status: SetupStatus;
  submitSetup(submission: SetupSubmission): Promise<SetupSubmissionResult>;
}

export function SetupContent({
  onComplete,
  onRetry,
  status,
  submitSetup,
}: SetupContentProps) {
  if (status.kind === "loading") {
    return (
      <section className={styles.routeState} aria-labelledby="setup-status-heading">
        <h2 id="setup-status-heading">{message("webapp.setup.heading")}</h2>
        <p role="status" aria-live="polite">
          {message("webapp.setup.loading")}
        </p>
      </section>
    );
  }
  if (status.kind === "failure") {
    return (
      <SetupRouteState
        heading={message("webapp.setup.status_failure.heading")}
        body={message("webapp.setup.status_failure.body")}
      >
        <Button onClick={onRetry}>
          {message("webapp.setup.status_failure.retry")}
        </Button>
      </SetupRouteState>
    );
  }
  if (status.kind === "complete") {
    return (
      <SetupRouteState
        heading={message("webapp.setup.already_initialized.heading")}
        body={message("webapp.setup.already_initialized.body")}
      >
        <ButtonLink href="/login">
          {message("webapp.setup.sign_in")}
        </ButtonLink>
      </SetupRouteState>
    );
  }
  return <SetupForm onComplete={onComplete} submitSetup={submitSetup} />;
}

function SetupRouteState({
  body,
  children,
  heading,
}: {
  body: string;
  children: ReactNode;
  heading: string;
}) {
  return (
    <section className={styles.routeState} aria-labelledby="setup-status-heading">
      <div className={styles.routeStateCopy}>
        <h2 id="setup-status-heading">{heading}</h2>
        <p>{body}</p>
      </div>
      {children}
    </section>
  );
}
