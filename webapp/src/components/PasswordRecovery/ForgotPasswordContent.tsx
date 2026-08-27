import { type FormEvent, useRef, useState } from "react";

import type { PasswordResetRequestResult } from "../../features/forgot-password/ForgotPasswordApi";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { Button } from "../Button/Button";
import { FormFeedback } from "../FormFeedback/FormFeedback";
import { InputField } from "../InputField/InputField";
import { Notice } from "../Notice/Notice";
import styles from "./PasswordRecovery.module.css";

export interface ForgotPasswordContentProps {
  requestReset(email: string): Promise<PasswordResetRequestResult>;
}

export function ForgotPasswordContent({
  requestReset,
}: ForgotPasswordContentProps) {
  const [email, setEmail] = useState("");
  const [emailError, setEmailError] = useState<string>();
  const [formError, setFormError] = useState<string>();
  const [pending, setPending] = useState(false);
  const [accepted, setAccepted] = useState(false);
  const emailRef = useRef<HTMLInputElement>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) {
      return;
    }
    if (email.trim() === "") {
      setEmailError(message("webapp.forgot_password.form.error.email_required"));
      emailRef.current?.focus();
      return;
    }
    if (emailRef.current?.validity.typeMismatch) {
      setEmailError(message("webapp.forgot_password.form.error.email_invalid"));
      emailRef.current.focus();
      return;
    }

    setPending(true);
    setEmailError(undefined);
    setFormError(undefined);
    const result = await requestReset(email);
    if (result.kind === "accepted") {
      setEmail("");
      setAccepted(true);
    } else if (result.kind === "rate_limited") {
      setFormError(message("webapp.forgot_password.form.error.rate_limited"));
    } else {
      setFormError(message("webapp.forgot_password.form.error.unavailable"));
    }
    setPending(false);
  }

  if (accepted) {
    return (
      <section className={styles.routeState} aria-labelledby="forgot-heading">
        <p className={styles.stateLabel}>
          {message("webapp.forgot_password.accepted.label")}
        </p>
        <h1 id="forgot-heading">
          {message("webapp.forgot_password.accepted.heading")}
        </h1>
        <p>{message("webapp.forgot_password.accepted.body")}</p>
        <Notice role="note">
          {message("webapp.forgot_password.accepted.note")}
        </Notice>
        <a className={styles.signInLink} href="/login">
          {message("webapp.forgot_password.sign_in")}
        </a>
      </section>
    );
  }

  return (
    <section className={styles.page} aria-labelledby="forgot-heading">
      <AccessTaskIntro
        eyebrow={message("webapp.forgot_password.context.eyebrow")}
        heading={message("webapp.forgot_password.context.heading")}
        body={message("webapp.forgot_password.context.body")}
        headingID="forgot-heading"
      />
      <header className={styles.headingGroup}>
        <h2>{message("webapp.forgot_password.heading")}</h2>
        <p>{message("webapp.forgot_password.lede")}</p>
      </header>
      <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
        <InputField
          ref={emailRef}
          id="password-reset-email"
          name="email"
          label={message("webapp.forgot_password.form.email")}
          type="email"
          inputMode="email"
          autoCapitalize="none"
          autoComplete="email"
          spellCheck={false}
          value={email}
          errorMessage={emailError}
          required
          onChange={(event) => {
            setEmail(event.currentTarget.value);
            setEmailError(undefined);
            setFormError(undefined);
          }}
        />
        <Notice role="note">
          {message("webapp.forgot_password.context.note")}
        </Notice>
        <div className={styles.actionRow}>
          <Button
            type="submit"
            isLoading={pending}
            loadingLabel={message("webapp.forgot_password.form.submitting")}
          >
            {message("webapp.forgot_password.form.submit")}
          </Button>
          <a className={styles.signInLink} href="/login">
            {message("webapp.forgot_password.sign_in")}
          </a>
        </div>
        <FormFeedback message={formError} />
      </form>
    </section>
  );
}
