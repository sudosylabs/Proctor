import type { ReactNode } from "react";

import type {
  SetupStatus,
  SetupSubmission,
  SetupSubmissionResult,
} from "../../features/setup/SetupApi";
import { message } from "../../i18n/messages";
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
      <section className={styles.routeState} aria-labelledby="setup-heading">
        <h1 id="setup-heading">{message("webapp.setup.heading")}</h1>
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
        <button className={styles.primaryButton} type="button" onClick={onRetry}>
          {message("webapp.setup.status_failure.retry")}
        </button>
      </SetupRouteState>
    );
  }
  if (status.kind === "complete") {
    return (
      <SetupRouteState
        heading={message("webapp.setup.already_initialized.heading")}
        body={message("webapp.setup.already_initialized.body")}
      >
        <a className={styles.primaryLink} href="/login">
          {message("webapp.setup.sign_in")}
        </a>
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
    <section className={styles.routeState} aria-labelledby="setup-heading">
      <div className={styles.routeStateCopy}>
        <h1 id="setup-heading">{heading}</h1>
        <p>{body}</p>
      </div>
      {children}
    </section>
  );
}
