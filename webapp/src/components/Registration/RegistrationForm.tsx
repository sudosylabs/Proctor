import { type FormEvent, useRef, useState } from "react";

import type {
  Discovery,
  RegistrationDiscoveryState,
  RegistrationSubmission,
  RegistrationSubmissionResult,
} from "../../features/register/RegistrationApi";
import { message } from "../../i18n/messages";
import { Icon } from "../Icon/Icon";
import styles from "./Registration.module.css";

type RegistrationField = "email" | "username" | "password" | "acknowledgment";
type FieldErrors = Partial<Record<RegistrationField, string>>;

export interface RegistrationFormProps {
  discovery: Discovery;
  onAccepted(): void;
  onPolicyChange(state: RegistrationDiscoveryState): void;
  submitRegistration(
    submission: RegistrationSubmission,
  ): Promise<RegistrationSubmissionResult>;
}

export function RegistrationForm({
  discovery,
  onAccepted,
  onPolicyChange,
  submitRegistration,
}: RegistrationFormProps) {
  const [email, setEmail] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [pending, setPending] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string>();
  const [liveMessage, setLiveMessage] = useState("");

  const emailRef = useRef<HTMLInputElement>(null);
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const acknowledgmentRef = useRef<HTMLInputElement>(null);
  const errorSummaryRef = useRef<HTMLDivElement>(null);

  const fieldRefs: Record<RegistrationField, React.RefObject<HTMLInputElement | null>> = {
    email: emailRef,
    username: usernameRef,
    password: passwordRef,
    acknowledgment: acknowledgmentRef,
  };

  function clearFieldError(field: RegistrationField) {
    setFieldErrors((errors) => ({ ...errors, [field]: undefined }));
    setFormError(undefined);
  }

  function focusField(field: RegistrationField) {
    requestAnimationFrame(() => fieldRefs[field].current?.focus());
  }

  function showFieldFailure(field: RegistrationField, error: string) {
    setFieldErrors((errors) => ({ ...errors, [field]: error }));
    setFormError(undefined);
    setLiveMessage(error);
    focusField(field);
  }

  function showFormFailure(error: string) {
    setFormError(error);
    setLiveMessage(error);
    requestAnimationFrame(() => errorSummaryRef.current?.focus());
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) {
      return;
    }

    const nextErrors: FieldErrors = {};
    if (email.trim() === "") {
      nextErrors.email = message("webapp.register.form.error.email_required");
    } else if (emailRef.current?.validity.typeMismatch) {
      nextErrors.email = message("webapp.register.form.error.email_invalid");
    }
    if (username.trim() === "") {
      nextErrors.username = message(
        "webapp.register.form.error.username_required",
      );
    }
    if (password === "") {
      nextErrors.password = message(
        "webapp.register.form.error.password_required",
      );
    }
    if (!acknowledged) {
      nextErrors.acknowledgment = message(
        "webapp.register.form.error.acknowledgment_required",
      );
    }
    setFieldErrors(nextErrors);
    setFormError(undefined);

    const firstInvalid = (
      ["email", "username", "password", "acknowledgment"] as const
    ).find((field) => nextErrors[field] !== undefined);
    if (firstInvalid !== undefined) {
      focusField(firstInvalid);
      return;
    }

    setPending(true);
    setLiveMessage(message("webapp.register.form.submitting"));
    try {
      const result = await submitRegistration({ email, username, password });

      if (result.kind === "accepted") {
        setEmail("");
        setUsername("");
        setPassword("");
        setAcknowledged(false);
        setLiveMessage(message("webapp.register.accepted.heading"));
        onAccepted();
        return;
      }

      if (result.kind === "failure") {
        showFormFailure(message("webapp.register.form.error.generic"));
        return;
      }

      switch (result.code) {
        case "authentication.password.invalid":
          showFieldFailure(
            "password",
            message("webapp.register.form.error.password_invalid"),
          );
          return;
        case "authentication.registration.invalid":
          showFormFailure(message("webapp.register.form.error.details_invalid"));
          return;
        case "authentication.registration.invitation_required":
          setPassword("");
          onPolicyChange({ kind: "invitation_required", discovery });
          return;
        case "authentication.registration.unavailable":
          setPassword("");
          onPolicyChange({ kind: "unavailable", discovery });
          return;
        case "authentication.rate_limited":
          showFormFailure(message("webapp.register.form.error.rate_limited"));
          return;
        default:
          showFormFailure(message("webapp.register.form.error.generic"));
      }
    } catch {
      showFormFailure(message("webapp.register.form.error.generic"));
    } finally {
      setPending(false);
    }
  }

  return (
    <section className={styles.page} aria-labelledby="register-heading">
      <header className={styles.headingGroup}>
        <h1 id="register-heading">{message("webapp.register.heading")}</h1>
        <p>{message("webapp.register.lede")}</p>
      </header>

      <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
        <div className={styles.visuallyHidden} aria-live="polite" aria-atomic="true">
          {liveMessage}
        </div>

        {formError === undefined ? null : (
          <div className={styles.formError} ref={errorSummaryRef} tabIndex={-1}>
            {formError}
          </div>
        )}

        <div className={styles.field}>
          <label htmlFor="registration-email">
            {message("webapp.register.form.email")}
          </label>
          <input
            ref={emailRef}
            id="registration-email"
            name="email"
            type="email"
            inputMode="email"
            autoComplete="email"
            value={email}
            aria-invalid={fieldErrors.email !== undefined}
            aria-describedby={
              fieldErrors.email === undefined ? undefined : "registration-email-error"
            }
            onChange={(event) => {
              setEmail(event.currentTarget.value);
              clearFieldError("email");
            }}
          />
          {fieldErrors.email === undefined ? null : (
            <p className={styles.fieldError} id="registration-email-error">
              {fieldErrors.email}
            </p>
          )}
        </div>

        <div className={styles.field}>
          <label htmlFor="registration-username">
            {message("webapp.register.form.username")}
          </label>
          <input
            ref={usernameRef}
            id="registration-username"
            name="username"
            type="text"
            autoComplete="username"
            value={username}
            aria-invalid={fieldErrors.username !== undefined}
            aria-describedby={
              fieldErrors.username === undefined
                ? undefined
                : "registration-username-error"
            }
            onChange={(event) => {
              setUsername(event.currentTarget.value);
              clearFieldError("username");
            }}
          />
          {fieldErrors.username === undefined ? null : (
            <p className={styles.fieldError} id="registration-username-error">
              {fieldErrors.username}
            </p>
          )}
        </div>

        <div className={styles.field}>
          <label htmlFor="registration-password">
            {message("webapp.register.form.password")}
          </label>
          <div className={styles.passwordControl}>
            <input
              ref={passwordRef}
              id="registration-password"
              name="password"
              type={showPassword ? "text" : "password"}
              autoComplete="new-password"
              value={password}
              aria-invalid={fieldErrors.password !== undefined}
              aria-describedby={
                fieldErrors.password === undefined
                  ? "registration-password-help"
                  : "registration-password-help registration-password-error"
              }
              onChange={(event) => {
                setPassword(event.currentTarget.value);
                clearFieldError("password");
              }}
            />
            <button
              className={styles.passwordToggle}
              type="button"
              aria-controls="registration-password"
              aria-pressed={showPassword}
              disabled={pending}
              onClick={() => setShowPassword((visible) => !visible)}
            >
              <Icon
                name={showPassword ? "hidePassword" : "showPassword"}
                size="small"
              />
              {message(
                showPassword
                  ? "webapp.register.form.password_hide"
                  : "webapp.register.form.password_show",
              )}
            </button>
          </div>
          <p className={styles.fieldHelp} id="registration-password-help">
            {message("webapp.register.form.password_help")}
          </p>
          {fieldErrors.password === undefined ? null : (
            <p className={styles.fieldError} id="registration-password-error">
              {fieldErrors.password}
            </p>
          )}
        </div>

        <div className={styles.acknowledgment}>
          <input
            ref={acknowledgmentRef}
            id="registration-acknowledgment"
            name="institutional_access_acknowledgment"
            type="checkbox"
            checked={acknowledged}
            aria-invalid={fieldErrors.acknowledgment !== undefined}
            aria-describedby={
              fieldErrors.acknowledgment === undefined
                ? undefined
                : "registration-acknowledgment-error"
            }
            onChange={(event) => {
              setAcknowledged(event.currentTarget.checked);
              clearFieldError("acknowledgment");
            }}
          />
          <div className={styles.acknowledgmentCopy}>
            <label htmlFor="registration-acknowledgment">
              {message("webapp.register.form.acknowledgment")}
            </label>
            {fieldErrors.acknowledgment === undefined ? null : (
              <p className={styles.fieldError} id="registration-acknowledgment-error">
                {fieldErrors.acknowledgment}
              </p>
            )}
          </div>
        </div>

        <button className={styles.primaryButton} type="submit" disabled={pending}>
          {message(
            pending
              ? "webapp.register.form.submitting"
              : "webapp.register.form.submit",
          )}
        </button>

        <div className={styles.signIn}>
          <p>{message("webapp.register.sign_in.prompt")}</p>
          <a href="/login">{message("webapp.register.sign_in.action")}</a>
        </div>

        <div className={styles.mailNote} role="note">
          <Icon className={styles.mailIcon} name="mail" />
          <p>{message("webapp.register.form.mail_note")}</p>
        </div>
      </form>
    </section>
  );
}
