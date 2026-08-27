import { useEffect, useRef, useState } from "react";

import { apiClient } from "../../api/client";
import { useAsyncResource } from "../../app/AsyncResource";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { Button, ButtonLink } from "../../components/Button/Button";
import { message } from "../../i18n/messages";
import styles from "./AuthorizationCompletePage.module.css";

type ConfirmationState = "checking" | "signed_in" | "no_session" | "unavailable";

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function isCurrentUser(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    value.id !== "" &&
    typeof value.username === "string" &&
    typeof value.display_name === "string"
  );
}

async function confirmSession(): Promise<ConfirmationState> {
  try {
    const { data, response } = await apiClient.GET("/api/v1/users/me");
    if (response.status === 200 && isCurrentUser(data)) {
      return "signed_in";
    }
    if (response.status === 401) {
      return "no_session";
    }
    return "unavailable";
  } catch {
    return "unavailable";
  }
}

export function AuthorizationCompletePage() {
  const confirmation = useAsyncResource<ConfirmationState>(
    confirmSession,
    "checking",
  );
  const state = confirmation.hasResolved ? confirmation.value : "checking";
  const retrying = confirmation.hasResolved && confirmation.loading;
  const [liveMessage, setLiveMessage] = useState(
    message("webapp.authorization_complete.checking.body"),
  );
  const retryRequested = useRef(false);
  const unavailableRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!confirmation.loading && confirmation.hasResolved) {
      setLiveMessage(stateBody(confirmation.value));
      if (retryRequested.current && confirmation.value === "unavailable") {
        requestAnimationFrame(() => unavailableRef.current?.focus());
      }
      retryRequested.current = false;
    }
  }, [confirmation.hasResolved, confirmation.loading, confirmation.value]);

  function retry() {
    if (confirmation.loading) {
      return;
    }
    retryRequested.current = true;
    setLiveMessage(message("webapp.authorization_complete.checking.body"));
    confirmation.retry();
  }

  return (
    <AccessPageShell
      skipLabel={message("webapp.authorization_complete.skip_to_main")}
      variant="status"
    >
      <div className={styles.page}>
        <div className={styles.visuallyHidden} aria-live="polite" aria-atomic="true">
          {liveMessage}
        </div>
        <StatusContent
          state={state}
          retrying={retrying}
          unavailableRef={unavailableRef}
          onRetry={retry}
        />
      </div>
    </AccessPageShell>
  );
}

function stateBody(state: ConfirmationState): string {
  switch (state) {
    case "checking":
      return message("webapp.authorization_complete.checking.body");
    case "signed_in":
      return message("webapp.authorization_complete.signed_in.body");
    case "no_session":
      return message("webapp.authorization_complete.no_session.body");
    case "unavailable":
      return message("webapp.authorization_complete.unavailable.body");
  }
}

function StatusContent({
  onRetry,
  retrying,
  state,
  unavailableRef,
}: {
  onRetry(): void;
  retrying: boolean;
  state: ConfirmationState;
  unavailableRef: React.RefObject<HTMLDivElement | null>;
}) {
  const content = statusContent(state);
  return (
    <div
      className={`${styles.status} ${styles[state]}`}
      ref={state === "unavailable" ? unavailableRef : undefined}
      tabIndex={state === "unavailable" ? -1 : undefined}
    >
      <div className={styles.rail} aria-hidden="true">
        <span />
      </div>
      <div className={styles.content}>
        <p className={styles.label}>{content.label}</p>
        <h1>{content.heading}</h1>
        <p className={styles.body}>{content.body}</p>
        {state === "no_session" ? (
          <div className={styles.actions}>
            <ButtonLink href="/login">
              {message("webapp.authorization_complete.sign_in")}
            </ButtonLink>
          </div>
        ) : null}
        {state === "unavailable" ? (
          <div className={styles.actions}>
            <Button
              isLoading={retrying}
              loadingLabel={message(
                "webapp.authorization_complete.checking.label",
              )}
              onClick={onRetry}
            >
              {message("webapp.authorization_complete.unavailable.retry")}
            </Button>
            <ButtonLink href="/login" variant="secondary">
              {message("webapp.authorization_complete.sign_in")}
            </ButtonLink>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function statusContent(state: ConfirmationState) {
  switch (state) {
    case "checking":
      return {
        label: message("webapp.authorization_complete.checking.label"),
        heading: message("webapp.authorization_complete.checking.heading"),
        body: message("webapp.authorization_complete.checking.body"),
      };
    case "signed_in":
      return {
        label: message("webapp.authorization_complete.signed_in.label"),
        heading: message("webapp.authorization_complete.signed_in.heading"),
        body: message("webapp.authorization_complete.signed_in.body"),
      };
    case "no_session":
      return {
        label: message("webapp.authorization_complete.no_session.label"),
        heading: message("webapp.authorization_complete.no_session.heading"),
        body: message("webapp.authorization_complete.no_session.body"),
      };
    case "unavailable":
      return {
        label: message("webapp.authorization_complete.unavailable.label"),
        heading: message("webapp.authorization_complete.unavailable.heading"),
        body: message("webapp.authorization_complete.unavailable.body"),
      };
  }
}
