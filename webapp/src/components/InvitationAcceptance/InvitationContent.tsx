import { type FormEvent, useRef, useState } from "react";

import type {
  InvitationAccountAcceptanceResult,
  InvitationAccountSubmission,
  InvitationSessionAcceptanceResult,
  InvitationStartResult,
} from "../../features/join/InvitationApi";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { Button, ButtonLink } from "../Button/Button";
import { FormFeedback } from "../FormFeedback/FormFeedback";
import { InputField } from "../InputField/InputField";
import { PasswordField } from "../InputField/PasswordField";
import { Notice } from "../Notice/Notice";
import {
  TaskState,
  TaskStateActions,
  TaskStateAnnouncement,
} from "../TaskState/TaskState";
import styles from "./InvitationAcceptance.module.css";

export interface InvitationContentProps {
  acceptAccount(
    submission: InvitationAccountSubmission,
  ): Promise<InvitationAccountAcceptanceResult>;
  acceptSession(handle: string): Promise<InvitationSessionAcceptanceResult>;
  institutionName?: string;
  loading: boolean;
  state: InvitationStartResult;
}

export function InvitationContent({
  acceptAccount,
  acceptSession,
  institutionName,
  loading,
  state,
}: InvitationContentProps) {
  const [accepted, setAccepted] = useState(false);

  if (accepted) {
    return (
      <>
        <TaskStateAnnouncement message={message("webapp.join.accepted.heading")} />
        <InvitationRouteState
          focusHeading
          label={message("webapp.join.accepted.label")}
          heading={message("webapp.join.accepted.heading")}
          body={message("webapp.join.accepted.body")}
        >
          <TaskStateActions>
            <ButtonLink href="/login">
              {message("webapp.join.sign_in")}
            </ButtonLink>
          </TaskStateActions>
        </InvitationRouteState>
      </>
    );
  }
  if (loading) {
    return (
      <>
        <TaskStateAnnouncement message={message("webapp.join.loading.heading")} />
        <InvitationRouteState
          busy
          heading={message("webapp.join.loading.heading")}
          body={message("webapp.join.loading.body")}
        />
      </>
    );
  }
  if (state.kind === "invalid") {
    return (
      <>
        <TaskStateAnnouncement message={message("webapp.join.invalid.heading")} />
        <InvitationRouteState
          label={message("webapp.join.invalid.label")}
          heading={message("webapp.join.invalid.heading")}
          body={message("webapp.join.invalid.body")}
        >
          <TaskStateActions>
            <ButtonLink href="/login" variant="secondary">
              {message("webapp.join.sign_in")}
            </ButtonLink>
          </TaskStateActions>
        </InvitationRouteState>
      </>
    );
  }
  if (state.kind === "unavailable") {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.join.unavailable.heading")}
        />
        <InvitationRouteState
          label={message("webapp.join.unavailable.label")}
          heading={message("webapp.join.unavailable.heading")}
          body={message("webapp.join.unavailable.body")}
        >
          <TaskStateActions>
            <Button onClick={() => window.location.reload()}>
              {message("webapp.join.unavailable.retry")}
            </Button>
          </TaskStateActions>
        </InvitationRouteState>
      </>
    );
  }
  if (state.transaction.requirement === "session") {
    return (
      <>
        <TaskStateAnnouncement message={message("webapp.join.session.heading")} />
        <SessionInvitation
          handle={state.transaction.handle}
          institutionName={institutionName}
          acceptSession={acceptSession}
          onAccepted={() => setAccepted(true)}
        />
      </>
    );
  }
  return (
    <>
      <TaskStateAnnouncement message={message("webapp.join.heading")} />
      <InvitationAccountForm
        handle={state.transaction.handle}
        institutionName={institutionName}
        acceptAccount={acceptAccount}
        onAccepted={() => setAccepted(true)}
      />
    </>
  );
}

function InvitationAccountForm({
  acceptAccount,
  handle,
  institutionName,
  onAccepted,
}: {
  acceptAccount(
    submission: InvitationAccountSubmission,
  ): Promise<InvitationAccountAcceptanceResult>;
  handle: string;
  institutionName?: string;
  onAccepted(): void;
}) {
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [usernameError, setUsernameError] = useState<string>();
  const [passwordError, setPasswordError] = useState<string>();
  const [formError, setFormError] = useState<string>();
  const [pending, setPending] = useState(false);
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) {
      return;
    }
    if (username.trim() === "") {
      setUsernameError(message("webapp.join.form.error.username_required"));
      usernameRef.current?.focus();
      return;
    }
    if (password === "") {
      setPasswordError(message("webapp.join.form.error.password_required"));
      passwordRef.current?.focus();
      return;
    }

    setPending(true);
    setFormError(undefined);
    const result = await acceptAccount({
      firstName,
      handle,
      lastName,
      password,
      username,
    });
    if (result.kind === "accepted") {
      setFirstName("");
      setLastName("");
      setUsername("");
      setPassword("");
      onAccepted();
    } else if (result.kind === "password_rejected") {
      setPasswordError(message("webapp.join.form.error.password_invalid"));
      requestAnimationFrame(() => passwordRef.current?.focus());
    } else if (result.kind === "details_invalid") {
      setFormError(message("webapp.join.form.error.details_invalid"));
    } else if (result.kind === "rate_limited") {
      setFormError(message("webapp.join.form.error.rate_limited"));
    } else {
      setFormError(message("webapp.join.form.error.unavailable"));
    }
    setPending(false);
  }

  return (
    <section className={styles.page} aria-labelledby="join-heading">
      <InvitationIntro
        headingID="join-heading"
        institutionName={institutionName}
        requirement="account"
      />
      <header className={styles.headingGroup}>
        <h2>{message("webapp.join.heading")}</h2>
        <p>{message("webapp.join.lede")}</p>
      </header>
      <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
        <div className={styles.nameFields}>
          <InputField
            id="invitation-first-name"
            name="first_name"
            label={message("webapp.join.form.first_name")}
            autoComplete="given-name"
            value={firstName}
            onChange={(event) => {
              setFirstName(event.currentTarget.value);
              setFormError(undefined);
            }}
          />
          <InputField
            id="invitation-last-name"
            name="last_name"
            label={message("webapp.join.form.last_name")}
            autoComplete="family-name"
            value={lastName}
            onChange={(event) => {
              setLastName(event.currentTarget.value);
              setFormError(undefined);
            }}
          />
        </div>
        <InputField
          ref={usernameRef}
          id="invitation-username"
          name="username"
          label={message("webapp.join.form.username")}
          autoCapitalize="none"
          autoComplete="username"
          spellCheck={false}
          value={username}
          errorMessage={usernameError}
          required
          onChange={(event) => {
            setUsername(event.currentTarget.value);
            setUsernameError(undefined);
            setFormError(undefined);
          }}
        />
        <PasswordField
          ref={passwordRef}
          id="invitation-password"
          name="password"
          label={message("webapp.join.form.password")}
          description={message("webapp.join.form.password_help")}
          autoComplete="new-password"
          value={password}
          errorMessage={passwordError}
          hidePasswordLabel={message("webapp.form.password_hide")}
          showPasswordLabel={message("webapp.form.password_show")}
          toggleDisabled={pending}
          required
          onChange={(event) => {
            setPassword(event.currentTarget.value);
            setPasswordError(undefined);
            setFormError(undefined);
          }}
        />
        <Notice role="note">
          {message("webapp.join.context.note_account")}
        </Notice>
        <div className={styles.actionRow}>
          <Button
            type="submit"
            isLoading={pending}
            loadingLabel={message("webapp.join.form.submitting")}
          >
            {message("webapp.join.form.submit")}
          </Button>
          <a className={styles.signInLink} href="/login">
            {message("webapp.join.sign_in")}
          </a>
        </div>
        <FormFeedback message={formError} />
      </form>
    </section>
  );
}

function SessionInvitation({
  acceptSession,
  handle,
  institutionName,
  onAccepted,
}: {
  acceptSession(handle: string): Promise<InvitationSessionAcceptanceResult>;
  handle: string;
  institutionName?: string;
  onAccepted(): void;
}) {
  const [pending, setPending] = useState(false);
  const [formError, setFormError] = useState<string>();
  const [needsSession, setNeedsSession] = useState(false);

  async function submit() {
    if (pending) {
      return;
    }
    setPending(true);
    setFormError(undefined);
    const result = await acceptSession(handle);
    if (result.kind === "accepted") {
      onAccepted();
    } else if (result.kind === "session_required") {
      setNeedsSession(true);
      setFormError(message("webapp.join.session.error.sign_in"));
    } else {
      setFormError(message("webapp.join.session.error.unavailable"));
    }
    setPending(false);
  }

  return (
    <section className={styles.page} aria-labelledby="join-heading">
      <InvitationIntro
        headingID="join-heading"
        institutionName={institutionName}
        requirement="session"
      />
      <header className={styles.headingGroup}>
        <h2>{message("webapp.join.session.heading")}</h2>
        <p>{message("webapp.join.session.lede")}</p>
      </header>
      <Notice role="note">
        {message("webapp.join.context.note_session")}
      </Notice>
      <div className={styles.sessionActions}>
        <Button
          isLoading={pending}
          loadingLabel={message("webapp.join.session.submitting")}
          onClick={submit}
        >
          {message("webapp.join.session.submit")}
        </Button>
        {needsSession ? (
          <a
            aria-describedby="join-session-feedback"
            className={styles.signInLink}
            href="/login"
            rel="noopener noreferrer"
            target="_blank"
          >
            {message("webapp.join.session.open_sign_in")}
          </a>
        ) : (
          <a className={styles.signInLink} href="/login">
            {message("webapp.join.sign_in")}
          </a>
        )}
      </div>
      <FormFeedback id="join-session-feedback" message={formError} />
    </section>
  );
}

function InvitationIntro({
  headingID,
  institutionName,
  requirement,
}: {
  headingID: string;
  institutionName?: string;
  requirement: "account" | "session";
}) {
  return (
    <AccessTaskIntro
      eyebrow={message("webapp.join.context.eyebrow")}
      heading={
        institutionName === undefined
          ? message("webapp.join.context.heading_fallback")
          : message("webapp.join.context.heading", {
              Institution: institutionName,
            })
      }
      body={message(
        requirement === "account"
          ? "webapp.join.context.body_account"
          : "webapp.join.context.body_session",
      )}
      headingID={headingID}
    />
  );
}

function InvitationRouteState({
  body,
  busy = false,
  children,
  focusHeading = false,
  heading,
  label,
}: {
  body: string;
  busy?: boolean;
  children?: React.ReactNode;
  focusHeading?: boolean;
  heading: string;
  label?: string;
}) {
  return (
    <TaskState
      body={body}
      busy={busy}
      className={styles.taskState}
      focusHeading={focusHeading}
      heading={heading}
      headingID="join-heading"
      label={label}
    >
      {children}
    </TaskState>
  );
}
