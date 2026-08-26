import { type FormEvent, useRef, useState } from "react";

import type {
  SetupSubmission,
  SetupSubmissionResult,
} from "../../features/setup/SetupApi";
import { message } from "../../i18n/messages";
import { Icon } from "../Icon/Icon";
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
  const [showSecret, setShowSecret] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
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
      <header className={styles.headingGroup}>
        <h1 id="setup-heading">{message("webapp.setup.heading")}</h1>
        <p>{message("webapp.setup.lede")}</p>
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

        <fieldset className={styles.section}>
          <legend>{message("webapp.setup.form.operator.heading")}</legend>
          <p className={styles.sectionHelp}>
            {message("webapp.setup.form.operator.body")}
          </p>
          <div className={styles.field}>
            <label htmlFor="bootstrap-secret">
              {message("webapp.setup.form.operator.secret")}
            </label>
            <div className={styles.passwordControl}>
              <input
                ref={bootstrapSecretRef}
                id="bootstrap-secret"
                name="bootstrap_secret"
                type={showSecret ? "text" : "password"}
                autoComplete="off"
                value={bootstrapSecret}
                aria-invalid={fieldErrors.bootstrap_secret !== undefined}
                aria-describedby={
                  fieldErrors.bootstrap_secret === undefined
                    ? undefined
                    : "bootstrap-secret-error"
                }
                onChange={(event) => {
                  setBootstrapSecret(event.currentTarget.value);
                  clearFieldError("bootstrap_secret");
                }}
              />
              <button
                className={styles.passwordToggle}
                type="button"
                aria-controls="bootstrap-secret"
                aria-pressed={showSecret}
                disabled={pending}
                onClick={() => setShowSecret((visible) => !visible)}
              >
                <Icon
                  name={showSecret ? "hidePassword" : "showPassword"}
                  size="small"
                />
                {message(
                  showSecret
                    ? "webapp.setup.form.secret_hide"
                    : "webapp.setup.form.secret_show",
                )}
              </button>
            </div>
            {fieldErrors.bootstrap_secret === undefined ? null : (
              <p className={styles.fieldError} id="bootstrap-secret-error">
                {fieldErrors.bootstrap_secret}
              </p>
            )}
          </div>
        </fieldset>

        <fieldset className={styles.section}>
          <legend>{message("webapp.setup.form.institution.heading")}</legend>
          <div className={styles.fieldGrid}>
            <div className={styles.field}>
              <label htmlFor="institution-name">
                {message("webapp.setup.form.institution.name")}
              </label>
              <input
                ref={institutionNameRef}
                id="institution-name"
                name="institution_name"
                type="text"
                autoComplete="off"
                value={institutionName}
                aria-invalid={fieldErrors.institution_name !== undefined}
                aria-describedby={
                  fieldErrors.institution_name === undefined
                    ? undefined
                    : "institution-name-error"
                }
                onChange={(event) => {
                  setInstitutionName(event.currentTarget.value);
                  clearFieldError("institution_name");
                }}
              />
              {fieldErrors.institution_name === undefined ? null : (
                <p className={styles.fieldError} id="institution-name-error">
                  {fieldErrors.institution_name}
                </p>
              )}
            </div>
            <div className={styles.field}>
              <label htmlFor="institution-display-name">
                {message("webapp.setup.form.institution.display_name")}
              </label>
              <input
                ref={institutionDisplayNameRef}
                id="institution-display-name"
                name="institution_display_name"
                type="text"
                autoComplete="organization"
                value={institutionDisplayName}
                aria-invalid={fieldErrors.institution_display_name !== undefined}
                aria-describedby={
                  fieldErrors.institution_display_name === undefined
                    ? undefined
                    : "institution-display-name-error"
                }
                onChange={(event) => {
                  setInstitutionDisplayName(event.currentTarget.value);
                  clearFieldError("institution_display_name");
                }}
              />
              {fieldErrors.institution_display_name === undefined ? null : (
                <p className={styles.fieldError} id="institution-display-name-error">
                  {fieldErrors.institution_display_name}
                </p>
              )}
            </div>
          </div>
          <div className={styles.field}>
            <label htmlFor="institution-description">
              {message("webapp.setup.form.institution.description")}
            </label>
            <input
              id="institution-description"
              name="institution_description"
              type="text"
              autoComplete="off"
              value={institutionDescription}
              onChange={(event) => setInstitutionDescription(event.currentTarget.value)}
            />
          </div>
        </fieldset>

        <fieldset className={styles.section}>
          <legend>{message("webapp.setup.form.administrator.heading")}</legend>
          <div className={styles.fieldGrid}>
            <div className={styles.field}>
              <label htmlFor="administrator-email">
                {message("webapp.setup.form.administrator.email")}
              </label>
              <input
                ref={administratorEmailRef}
                id="administrator-email"
                name="administrator_email"
                type="email"
                inputMode="email"
                autoComplete="email"
                value={administratorEmail}
                aria-invalid={fieldErrors.administrator_email !== undefined}
                aria-describedby={
                  fieldErrors.administrator_email === undefined
                    ? undefined
                    : "administrator-email-error"
                }
                onChange={(event) => {
                  setAdministratorEmail(event.currentTarget.value);
                  clearFieldError("administrator_email");
                }}
              />
              {fieldErrors.administrator_email === undefined ? null : (
                <p className={styles.fieldError} id="administrator-email-error">
                  {fieldErrors.administrator_email}
                </p>
              )}
            </div>
            <div className={styles.field}>
              <label htmlFor="administrator-username">
                {message("webapp.setup.form.administrator.username")}
              </label>
              <input
                ref={administratorUsernameRef}
                id="administrator-username"
                name="administrator_username"
                type="text"
                autoComplete="username"
                value={administratorUsername}
                aria-invalid={fieldErrors.administrator_username !== undefined}
                aria-describedby={
                  fieldErrors.administrator_username === undefined
                    ? undefined
                    : "administrator-username-error"
                }
                onChange={(event) => {
                  setAdministratorUsername(event.currentTarget.value);
                  clearFieldError("administrator_username");
                }}
              />
              {fieldErrors.administrator_username === undefined ? null : (
                <p className={styles.fieldError} id="administrator-username-error">
                  {fieldErrors.administrator_username}
                </p>
              )}
            </div>
            <div className={styles.field}>
              <label htmlFor="administrator-display-name">
                {message("webapp.setup.form.administrator.display_name")}
              </label>
              <input
                id="administrator-display-name"
                name="administrator_display_name"
                type="text"
                autoComplete="name"
                value={administratorDisplayName}
                onChange={(event) => setAdministratorDisplayName(event.currentTarget.value)}
              />
            </div>
            <div className={styles.field}>
              <label htmlFor="administrator-password">
                {message("webapp.setup.form.administrator.password")}
              </label>
              <div className={styles.passwordControl}>
                <input
                  ref={passwordRef}
                  id="administrator-password"
                  name="password"
                  type={showPassword ? "text" : "password"}
                  autoComplete="new-password"
                  value={password}
                  aria-invalid={fieldErrors.password !== undefined}
                  aria-describedby={
                    fieldErrors.password === undefined
                      ? "administrator-password-help"
                      : "administrator-password-help administrator-password-error"
                  }
                  onChange={(event) => {
                    setPassword(event.currentTarget.value);
                    clearFieldError("password");
                  }}
                />
                <button
                  className={styles.passwordToggle}
                  type="button"
                  aria-controls="administrator-password"
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
                      ? "webapp.setup.form.password_hide"
                      : "webapp.setup.form.password_show",
                  )}
                </button>
              </div>
              <p className={styles.fieldHelp} id="administrator-password-help">
                {message("webapp.setup.form.password_help")}
              </p>
              {fieldErrors.password === undefined ? null : (
                <p className={styles.fieldError} id="administrator-password-error">
                  {fieldErrors.password}
                </p>
              )}
            </div>
          </div>
        </fieldset>

        <div className={styles.caution} role="note">
          <Icon className={styles.cautionIcon} name="warning" />
          <p>{message("webapp.setup.form.caution")}</p>
        </div>

        <div className={styles.actions}>
          <button className={styles.primaryButton} type="submit" disabled={pending}>
            {message(
              pending
                ? "webapp.setup.form.submitting"
                : "webapp.setup.form.submit",
            )}
          </button>
          <a href="/login">{message("webapp.setup.return_to_sign_in")}</a>
        </div>
      </form>
    </section>
  );
}
