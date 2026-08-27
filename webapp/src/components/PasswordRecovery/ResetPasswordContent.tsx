import { type FormEvent, useRef, useState } from "react";

import type { PasswordResetCompletionResult } from "../../features/reset-password/ResetPasswordApi";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { Button } from "../Button/Button";
import { FormFeedback } from "../FormFeedback/FormFeedback";
import { PasswordField } from "../InputField/PasswordField";
import { Notice } from "../Notice/Notice";
import styles from "./PasswordRecovery.module.css";

type ResetState = "ready" | "complete" | "invalid";

export interface ResetPasswordContentProps {
  completeReset(
    token: string,
    password: string,
  ): Promise<PasswordResetCompletionResult>;
  token?: string;
}

export function ResetPasswordContent({
  completeReset,
  token,
}: ResetPasswordContentProps) {
  const tokenRef = useRef(token);
  const [state, setState] = useState<ResetState>(
    token === undefined ? "invalid" : "ready",
  );
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [passwordError, setPasswordError] = useState<string>();
  const [confirmationError, setConfirmationError] = useState<string>();
  const [formError, setFormError] = useState<string>();
  const [pending, setPending] = useState(false);
  const passwordRef = useRef<HTMLInputElement>(null);
  const confirmationRef = useRef<HTMLInputElement>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const currentToken = tokenRef.current;
    if (pending || currentToken === undefined) {
      return;
    }
    if (password === "") {
      setPasswordError(message("webapp.reset_password.form.error.password_required"));
      passwordRef.current?.focus();
      return;
    }
    if (confirmation === "") {
      setConfirmationError(
        message("webapp.reset_password.form.error.confirmation_required"),
      );
      confirmationRef.current?.focus();
      return;
    }
    if (confirmation !== password) {
      setConfirmationError(message("webapp.reset_password.form.error.mismatch"));
      confirmationRef.current?.focus();
      return;
    }

    setPending(true);
    setPasswordError(undefined);
    setConfirmationError(undefined);
    setFormError(undefined);
    const result = await completeReset(currentToken, password);
    if (result.kind === "complete") {
      tokenRef.current = undefined;
      setPassword("");
      setConfirmation("");
      setState("complete");
    } else if (result.kind === "invalid") {
      tokenRef.current = undefined;
      setPassword("");
      setConfirmation("");
      setState("invalid");
    } else if (result.kind === "password_rejected") {
      setPasswordError(message("webapp.reset_password.form.error.password_invalid"));
      requestAnimationFrame(() => passwordRef.current?.focus());
    } else if (result.kind === "rate_limited") {
      setFormError(message("webapp.reset_password.form.error.rate_limited"));
    } else {
      setFormError(message("webapp.reset_password.form.error.unavailable"));
    }
    setPending(false);
  }

  if (state !== "ready") {
    return (
      <section className={styles.routeState} aria-labelledby="reset-heading">
        <p className={styles.stateLabel}>
          {message(
            state === "complete"
              ? "webapp.reset_password.complete.label"
              : "webapp.reset_password.invalid.label",
          )}
        </p>
        <h1 id="reset-heading">
          {message(
            state === "complete"
              ? "webapp.reset_password.complete.heading"
              : "webapp.reset_password.invalid.heading",
          )}
        </h1>
        <p>
          {message(
            state === "complete"
              ? "webapp.reset_password.complete.body"
              : "webapp.reset_password.invalid.body",
          )}
        </p>
        <Notice
          role="note"
          tone={state === "complete" ? "information" : "warning"}
        >
          {message(
            state === "complete"
              ? "webapp.reset_password.complete.note"
              : "webapp.reset_password.invalid.note",
          )}
        </Notice>
        <a className={styles.signInLink} href="/login">
          {message("webapp.reset_password.sign_in")}
        </a>
      </section>
    );
  }

  return (
    <section className={styles.page} aria-labelledby="reset-heading">
      <AccessTaskIntro
        eyebrow={message("webapp.reset_password.context.eyebrow")}
        heading={message("webapp.reset_password.context.heading")}
        body={message("webapp.reset_password.context.body")}
        headingID="reset-heading"
      />
      <header className={styles.headingGroup}>
        <h2>{message("webapp.reset_password.heading")}</h2>
        <p>{message("webapp.reset_password.lede")}</p>
      </header>
      <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
        <PasswordField
          ref={passwordRef}
          id="new-password"
          name="new_password"
          label={message("webapp.reset_password.form.password")}
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
        <PasswordField
          ref={confirmationRef}
          id="confirm-new-password"
          name="confirm_new_password"
          label={message("webapp.reset_password.form.confirmation")}
          description={message("webapp.reset_password.form.password_help")}
          autoComplete="new-password"
          value={confirmation}
          errorMessage={confirmationError}
          hidePasswordLabel={message("webapp.form.password_hide")}
          showPasswordLabel={message("webapp.form.password_show")}
          toggleDisabled={pending}
          required
          onChange={(event) => {
            setConfirmation(event.currentTarget.value);
            setConfirmationError(undefined);
            setFormError(undefined);
          }}
        />
        <Notice role="note">
          {message("webapp.reset_password.form.session_note")}
        </Notice>
        <div className={styles.actionRow}>
          <Button
            type="submit"
            isLoading={pending}
            loadingLabel={message("webapp.reset_password.form.submitting")}
          >
            {message("webapp.reset_password.form.submit")}
          </Button>
          <a className={styles.signInLink} href="/login">
            {message("webapp.reset_password.sign_in")}
          </a>
        </div>
        <FormFeedback message={formError} />
      </form>
    </section>
  );
}
