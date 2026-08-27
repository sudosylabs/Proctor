import { type FormEvent, useRef, useState } from "react";

import type {
  Discovery,
  RegistrationDiscoveryState,
  RegistrationSubmission,
  RegistrationSubmissionResult,
} from "../../features/register/RegistrationApi";
import { message } from "../../i18n/messages";
import { Button } from "../Button/Button";
import { InputField, RequiredMark } from "../InputField/InputField";
import { PasswordField } from "../InputField/PasswordField";
import { Notice } from "../Notice/Notice";
import styles from "./Registration.module.css";

type RegistrationField =
  | "firstName"
  | "lastName"
  | "email"
  | "username"
  | "password"
  | "acknowledgment";
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
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [email, setEmail] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);
  const [pending, setPending] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string>();
  const [liveMessage, setLiveMessage] = useState("");

  const firstNameRef = useRef<HTMLInputElement>(null);
  const lastNameRef = useRef<HTMLInputElement>(null);
  const emailRef = useRef<HTMLInputElement>(null);
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const acknowledgmentRef = useRef<HTMLInputElement>(null);
  const errorSummaryRef = useRef<HTMLDivElement>(null);

  const fieldRefs: Record<RegistrationField, React.RefObject<HTMLInputElement | null>> = {
    firstName: firstNameRef,
    lastName: lastNameRef,
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
    if (firstName.trim() === "") {
      nextErrors.firstName = message(
        "webapp.register.form.error.first_name_required",
      );
    }
    if (lastName.trim() === "") {
      nextErrors.lastName = message(
        "webapp.register.form.error.last_name_required",
      );
    }
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
      [
        "firstName",
        "lastName",
        "email",
        "username",
        "password",
        "acknowledgment",
      ] as const
    ).find((field) => nextErrors[field] !== undefined);
    if (firstInvalid !== undefined) {
      focusField(firstInvalid);
      return;
    }

    setPending(true);
    setLiveMessage(message("webapp.register.form.submitting"));
    try {
      const result = await submitRegistration({
        email,
        firstName,
        lastName,
        username,
        password,
      });

      if (result.kind === "accepted") {
        setFirstName("");
        setLastName("");
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
          <Notice ref={errorSummaryRef} tabIndex={-1} tone="danger">
            {formError}
          </Notice>
        )}

        <div className={styles.nameFields}>
          <InputField
            ref={firstNameRef}
            id="registration-first-name"
            name="first_name"
            label={message("webapp.register.form.first_name")}
            type="text"
            autoComplete="given-name"
            value={firstName}
            errorMessage={fieldErrors.firstName}
            required
            onChange={(event) => {
              setFirstName(event.currentTarget.value);
              clearFieldError("firstName");
            }}
          />

          <InputField
            ref={lastNameRef}
            id="registration-last-name"
            name="last_name"
            label={message("webapp.register.form.last_name")}
            type="text"
            autoComplete="family-name"
            value={lastName}
            errorMessage={fieldErrors.lastName}
            required
            onChange={(event) => {
              setLastName(event.currentTarget.value);
              clearFieldError("lastName");
            }}
          />
        </div>

        <InputField
          ref={emailRef}
          id="registration-email"
          name="email"
          label={message("webapp.register.form.email")}
          type="email"
          inputMode="email"
          autoCapitalize="none"
          autoComplete="email"
          spellCheck={false}
          value={email}
          errorMessage={fieldErrors.email}
          required
          onChange={(event) => {
            setEmail(event.currentTarget.value);
            clearFieldError("email");
          }}
        />

        <InputField
          ref={usernameRef}
          id="registration-username"
          name="username"
          label={message("webapp.register.form.username")}
          type="text"
          autoCapitalize="none"
          autoComplete="username"
          spellCheck={false}
          value={username}
          errorMessage={fieldErrors.username}
          required
          onChange={(event) => {
            setUsername(event.currentTarget.value);
            clearFieldError("username");
          }}
        />

        <PasswordField
          ref={passwordRef}
          id="registration-password"
          name="password"
          label={message("webapp.register.form.password")}
          description={message("webapp.register.form.password_help")}
          autoComplete="new-password"
          value={password}
          errorMessage={fieldErrors.password}
          hidePasswordLabel={message("webapp.form.password_hide")}
          showPasswordLabel={message("webapp.form.password_show")}
          toggleDisabled={pending}
          required
          onChange={(event) => {
            setPassword(event.currentTarget.value);
            clearFieldError("password");
          }}
        />

        <div className={styles.acknowledgment}>
          <label
            className={styles.checkboxLabel}
            htmlFor="registration-acknowledgment"
          >
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
              required
              onChange={(event) => {
                setAcknowledged(event.currentTarget.checked);
                clearFieldError("acknowledgment");
              }}
            />
            <span>
              {message("webapp.register.form.acknowledgment")}
              <RequiredMark />
            </span>
          </label>
          {fieldErrors.acknowledgment === undefined ? null : (
            <p
              className={styles.checkboxError}
              id="registration-acknowledgment-error"
            >
              {fieldErrors.acknowledgment}
            </p>
          )}
        </div>

        <Button
          type="submit"
          isLoading={pending}
          loadingLabel={message("webapp.register.form.submitting")}
        >
          {message("webapp.register.form.submit")}
        </Button>

        <div className={styles.signIn}>
          <p>{message("webapp.register.sign_in.prompt")}</p>
          <a href="/login">{message("webapp.register.sign_in.action")}</a>
        </div>

        <Notice role="note" tone="information">
          <p>{message("webapp.register.form.mail_note")}</p>
        </Notice>
      </form>
    </section>
  );
}
