import type { ReactNode } from "react";

import type {
  RegistrationDiscoveryState,
  RegistrationSubmission,
  RegistrationSubmissionResult,
} from "../../features/register/RegistrationApi";
import { message } from "../../i18n/messages";
import { Button, ButtonLink } from "../Button/Button";
import { RegistrationForm } from "./RegistrationForm";
import styles from "./Registration.module.css";

export interface RegistrationContentProps {
  accepted: boolean;
  onAccepted(): void;
  onPolicyChange(state: RegistrationDiscoveryState): void;
  onRetry(): void;
  state: RegistrationDiscoveryState;
  submitRegistration(
    submission: RegistrationSubmission,
  ): Promise<RegistrationSubmissionResult>;
}

export function RegistrationContent({
  accepted,
  onAccepted,
  onPolicyChange,
  onRetry,
  state,
  submitRegistration,
}: RegistrationContentProps) {
  if (accepted) {
    return (
      <RegistrationRouteState
        heading={message("webapp.register.accepted.heading")}
        body={message("webapp.register.accepted.body")}
        label={message("webapp.register.accepted.label")}
      >
        <ButtonLink href="/login">
          {message("webapp.register.sign_in.action")}
        </ButtonLink>
      </RegistrationRouteState>
    );
  }
  if (state.kind === "loading") {
    return (
      <section className={styles.routeState} aria-labelledby="register-heading">
        <h1 id="register-heading">{message("webapp.register.heading")}</h1>
        <p role="status" aria-live="polite">
          {message("webapp.register.loading")}
        </p>
      </section>
    );
  }
  if (state.kind === "setup") {
    return (
      <RegistrationRouteState
        heading={message("webapp.register.setup.heading")}
        body={message("webapp.register.setup.body")}
      >
        <ButtonLink href="/setup">
          {message("webapp.register.setup.open")}
        </ButtonLink>
      </RegistrationRouteState>
    );
  }
  if (state.kind === "invitation_required") {
    return (
      <RegistrationRouteState
        heading={message("webapp.register.invitation_required.heading")}
        body={message("webapp.register.invitation_required.body")}
      >
        <ButtonLink href="/login">
          {message("webapp.register.sign_in.action")}
        </ButtonLink>
      </RegistrationRouteState>
    );
  }
  if (state.kind === "origin_mismatch") {
    return (
      <RegistrationRouteState
        heading={message("webapp.register.origin_mismatch.heading")}
        body={message("webapp.register.origin_mismatch.body")}
      >
        <Button onClick={() => window.location.reload()}>
          {message("webapp.register.reload")}
        </Button>
      </RegistrationRouteState>
    );
  }
  if (state.kind === "failure" || state.kind === "unavailable") {
    const unavailable = state.kind === "unavailable";
    return (
      <RegistrationRouteState
        heading={message(
          unavailable
            ? "webapp.register.unavailable.heading"
            : "webapp.register.discovery_failure.heading",
        )}
        body={message(
          unavailable
            ? "webapp.register.unavailable.body"
            : "webapp.register.discovery_failure.body",
        )}
      >
        <Button onClick={onRetry}>
          {message("webapp.register.retry")}
        </Button>
      </RegistrationRouteState>
    );
  }
  return (
    <RegistrationForm
      discovery={state.discovery}
      onAccepted={onAccepted}
      onPolicyChange={onPolicyChange}
      submitRegistration={submitRegistration}
    />
  );
}

function RegistrationRouteState({
  body,
  children,
  heading,
  label,
}: {
  body: string;
  children: ReactNode;
  heading: string;
  label?: string;
}) {
  return (
    <section className={styles.routeState} aria-labelledby="register-heading">
      {label === undefined ? null : <p className={styles.stateLabel}>{label}</p>}
      <div className={styles.routeStateCopy}>
        <h1 id="register-heading">{heading}</h1>
        <p>{body}</p>
      </div>
      {children}
    </section>
  );
}
