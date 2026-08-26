import { type FormEvent, useRef, useState } from "react";

import type {
  SetupSubmission,
  SetupSubmissionResult,
} from "../../features/setup/SetupApi";
import { message } from "../../i18n/messages";
import { Button } from "../Button/Button";
import { Icon } from "../Icon/Icon";
import { InputField } from "../InputField/InputField";
import { PasswordField } from "../InputField/PasswordField";
import styles from "./Setup.module.css";

type SetupField =
  | "bootstrap_secret"
  | "institution_name"
  | "institution_display_name"
  | "administrator_email"
  | "administrator_username"
  | "password";

type FieldErrors = Partial<Record<SetupField, string>>;

export interface SetupFormProps {
  onComplete(): void;
  submitSetup(submission: SetupSubmission): Promise<SetupSubmissionResult>;
}

export function SetupForm({ onComplete, submitSetup }: SetupFormProps) {
  const [bootstrapSecret, setBootstrapSecret] = useState("");
  const [institutionName, setInstitutionName] = useState("");
  const [institutionDisplayName, setInstitutionDisplayName] = useState("");
  const [institutionDescription, setInstitutionDescription] = useState("");
  const [administratorEmail, setAdministratorEmail] = useState("");
  const [administratorUsername, setAdministratorUsername] = useState("");
  const [administratorDisplayName, setAdministratorDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string>();
  const [liveMessage, setLiveMessage] = useState("");

  const bootstrapSecretRef = useRef<HTMLInputElement>(null);
  const institutionNameRef = useRef<HTMLInputElement>(null);
  const institutionDisplayNameRef = useRef<HTMLInputElement>(null);
  const administratorEmailRef = useRef<HTMLInputElement>(null);
  const administratorUsernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const errorSummaryRef = useRef<HTMLDivElement>(null);

  const fieldRefs: Record<SetupField, React.RefObject<HTMLInputElement | null>> = {
    bootstrap_secret: bootstrapSecretRef,
    institution_name: institutionNameRef,
    institution_display_name: institutionDisplayNameRef,
    administrator_email: administratorEmailRef,
    administrator_username: administratorUsernameRef,
    password: passwordRef,
  };

  function clearFieldError(field: SetupField) {
    setFieldErrors((errors) => ({ ...errors, [field]: undefined }));
    setFormError(undefined);
  }

  function focusField(field: SetupField) {
    requestAnimationFrame(() => fieldRefs[field].current?.focus());
  }

  function showFieldFailure(field: SetupField, error: string) {
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
    if (bootstrapSecret === "") {
      nextErrors.bootstrap_secret = message(
        "webapp.setup.form.error.bootstrap_secret_required",
      );
    }
    if (institutionName.trim() === "") {
      nextErrors.institution_name = message(
        "webapp.setup.form.error.institution_name_required",
      );
    }
    if (institutionDisplayName.trim() === "") {
      nextErrors.institution_display_name = message(
        "webapp.setup.form.error.institution_display_name_required",
      );
    }
    if (administratorEmail.trim() === "") {
      nextErrors.administrator_email = message(
        "webapp.setup.form.error.email_required",
      );
    } else if (administratorEmailRef.current?.validity.typeMismatch) {
      nextErrors.administrator_email = message(
        "webapp.setup.form.error.email_invalid",
      );
    }
    if (administratorUsername.trim() === "") {
      nextErrors.administrator_username = message(
        "webapp.setup.form.error.username_required",
      );
    }
    if (password === "") {
      nextErrors.password = message(
        "webapp.setup.form.error.password_required",
      );
    }
    setFieldErrors(nextErrors);
    setFormError(undefined);

    const firstInvalid = (
      [
        "bootstrap_secret",
        "institution_name",
        "institution_display_name",
        "administrator_email",
        "administrator_username",
        "password",
      ] as const
    ).find((field) => nextErrors[field] !== undefined);
    if (firstInvalid !== undefined) {
      focusField(firstInvalid);
      return;
    }

    setPending(true);
    setLiveMessage(message("webapp.setup.form.submitting"));
    try {
      const result = await submitSetup({
        bootstrapSecret,
        institutionName,
        institutionDisplayName,
        institutionDescription,
        administratorEmail,
        administratorUsername,
        administratorDisplayName,
        password,
      });

      if (result.kind === "complete") {
        setBootstrapSecret("");
        setPassword("");
        setLiveMessage(message("webapp.setup.already_initialized.heading"));
        onComplete();
        return;
      }

      if (result.kind === "failure") {
        showFormFailure(message("webapp.setup.form.error.generic"));
        return;
      }

      switch (result.code) {
        case "installation.already_initialized":
          setBootstrapSecret("");
          setPassword("");
          onComplete();
          return;
        case "installation.bootstrap_denied":
          showFieldFailure(
            "bootstrap_secret",
            message("webapp.setup.form.error.bootstrap_denied"),
          );
          return;
        case "authentication.password.invalid":
          showFieldFailure(
            "password",
            message("webapp.setup.form.error.password_invalid"),
          );
          return;
        case "authentication.rate_limited":
          showFormFailure(message("webapp.setup.form.error.rate_limited"));
          return;
        default:
          showFormFailure(message("webapp.setup.form.error.generic"));
      }
    } catch {
      showFormFailure(message("webapp.setup.form.error.generic"));
    } finally {
      setPending(false);
    }
  }

  return (
    <section className={styles.page} aria-labelledby="setup-heading">
      <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
        <div className={styles.visuallyHidden} aria-live="polite" aria-atomic="true">
          {liveMessage}
        </div>

        {formError === undefined ? null : (
          <div className={styles.formError} ref={errorSummaryRef} tabIndex={-1}>
            {formError}
          </div>
        )}

        <section
          className={`${styles.section} ${styles.operatorSection}`}
          aria-labelledby="operator-verification-heading"
        >
          <div className={styles.sectionIntro}>
            <h2 id="operator-verification-heading">
              {message("webapp.setup.form.operator.heading")}
            </h2>
            <p className={styles.sectionHelp}>
              {message("webapp.setup.form.operator.body")}
            </p>
          </div>
          <PasswordField
            ref={bootstrapSecretRef}
            className={styles.operatorField}
            id="bootstrap-secret"
            name="bootstrap_secret"
            label={message("webapp.setup.form.operator.secret")}
            autoComplete="off"
            value={bootstrapSecret}
            errorMessage={fieldErrors.bootstrap_secret}
            hidePasswordLabel={message("webapp.setup.form.secret_hide")}
            showPasswordLabel={message("webapp.setup.form.secret_show")}
            toggleDisabled={pending}
            required
            onChange={(event) => {
              setBootstrapSecret(event.currentTarget.value);
              clearFieldError("bootstrap_secret");
            }}
          />
        </section>

        <section
          className={styles.section}
          aria-labelledby="institution-heading"
        >
          <h2 id="institution-heading">
            {message("webapp.setup.form.institution.heading")}
          </h2>
          <div className={styles.fieldGrid}>
            <InputField
              ref={institutionNameRef}
              id="institution-name"
              name="institution_name"
              label={message("webapp.setup.form.institution.name")}
              type="text"
              autoCapitalize="none"
              autoComplete="off"
              spellCheck={false}
              value={institutionName}
              errorMessage={fieldErrors.institution_name}
              required
              onChange={(event) => {
                setInstitutionName(event.currentTarget.value);
                clearFieldError("institution_name");
              }}
            />
            <InputField
              ref={institutionDisplayNameRef}
              id="institution-display-name"
              name="institution_display_name"
              label={message("webapp.setup.form.institution.display_name")}
              type="text"
              autoComplete="organization"
              value={institutionDisplayName}
              errorMessage={fieldErrors.institution_display_name}
              required
              onChange={(event) => {
                setInstitutionDisplayName(event.currentTarget.value);
                clearFieldError("institution_display_name");
              }}
            />
          </div>
          <InputField
            id="institution-description"
            name="institution_description"
            label={message("webapp.setup.form.institution.description")}
            type="text"
            autoComplete="off"
            value={institutionDescription}
            onChange={(event) => setInstitutionDescription(event.currentTarget.value)}
          />
        </section>

        <section
          className={`${styles.section} ${styles.administratorSection}`}
          aria-labelledby="administrator-heading"
        >
          <h2 id="administrator-heading">
            {message("webapp.setup.form.administrator.heading")}
          </h2>
          <div className={styles.fieldGrid}>
            <InputField
              ref={administratorEmailRef}
              id="administrator-email"
              name="administrator_email"
              label={message("webapp.setup.form.administrator.email")}
              type="email"
              inputMode="email"
              autoCapitalize="none"
              autoComplete="email"
              spellCheck={false}
              value={administratorEmail}
              errorMessage={fieldErrors.administrator_email}
              required
              onChange={(event) => {
                setAdministratorEmail(event.currentTarget.value);
                clearFieldError("administrator_email");
              }}
            />
            <InputField
              ref={administratorUsernameRef}
              id="administrator-username"
              name="administrator_username"
              label={message("webapp.setup.form.administrator.username")}
              type="text"
              autoCapitalize="none"
              autoComplete="username"
              spellCheck={false}
              value={administratorUsername}
              errorMessage={fieldErrors.administrator_username}
              required
              onChange={(event) => {
                setAdministratorUsername(event.currentTarget.value);
                clearFieldError("administrator_username");
              }}
            />
            <InputField
              id="administrator-display-name"
              name="administrator_display_name"
              label={message("webapp.setup.form.administrator.display_name")}
              type="text"
              autoComplete="name"
              value={administratorDisplayName}
              onChange={(event) => setAdministratorDisplayName(event.currentTarget.value)}
            />
            <PasswordField
              ref={passwordRef}
              id="administrator-password"
              name="password"
              label={message("webapp.setup.form.administrator.password")}
              description={message("webapp.setup.form.password_help")}
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
          </div>
        </section>

        <p className={styles.caution} role="note">
          <Icon className={styles.cautionIcon} name="warning" />
          <span>{message("webapp.setup.form.caution")}</span>
        </p>

        <div className={styles.actions}>
          <Button
            type="submit"
            isLoading={pending}
            loadingLabel={message("webapp.setup.form.submitting")}
          >
            {message("webapp.setup.form.submit")}
          </Button>
          <a href="/login">{message("webapp.setup.return_to_sign_in")}</a>
        </div>
      </form>
    </section>
  );
}
