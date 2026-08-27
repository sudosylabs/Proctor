import {
  type FormEvent,
  useEffect,
  useRef,
  useState,
} from "react";

import { navigateToProvider } from "../../auth/navigation";
import type { PublicAccessDiscovery } from "../../auth/PublicAccessDiscovery";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { Button, ButtonLink } from "../../components/Button/Button";
import { FormFeedback } from "../../components/FormFeedback/FormFeedback";
import { InputField } from "../../components/InputField/InputField";
import { PasswordField } from "../../components/InputField/PasswordField";
import { Notice } from "../../components/Notice/Notice";
import { message } from "../../i18n/messages";
import { authenticateLocal, type AuthenticateLocal } from "./LoginApi";
import {
  requestLoginDiscovery,
  type LoginDiscoveryState,
} from "./LoginDiscovery";
import styles from "./LoginPage.module.css";

type FieldErrors = Partial<Record<"login_id" | "password" | "mfa_code", string>>;

export interface LoginPageProps {
  externalLoginFailed: boolean;
}

function institutionName(state: LoginDiscoveryState): string | undefined {
  if (
    state.kind !== "ready" &&
    state.kind !== "setup" &&
    state.kind !== "unavailable"
  ) {
    return undefined;
  }
  return state.discovery.institution?.display_name;
}

function LoginContext({ state }: { state: LoginDiscoveryState }) {
  const name = institutionName(state);
  return (
    <div className={styles.context}>
      <p className={styles.contextLabel}>
        {message("webapp.login.institution.label")}
      </p>
      <p className={styles.contextName}>
        {name ?? message("webapp.login.institution.fallback")}
      </p>
      <div className={styles.contextRule} aria-hidden="true" />
      <p className={styles.contextBody}>
        {state.kind === "loading"
          ? message("webapp.login.institution.checking")
          : message("webapp.login.institution.body")}
      </p>
    </div>
  );
}

export function LoginPage({ externalLoginFailed }: LoginPageProps) {
  const [discoveryState, setDiscoveryState] = useState<LoginDiscoveryState>({
    kind: "loading",
  });
  const [discoveryAttempt, setDiscoveryAttempt] = useState(0);
  const discoveryRequest = useRef<{
    attempt: number;
    promise: Promise<LoginDiscoveryState>;
  } | undefined>(undefined);
  const [externalNotice, setExternalNotice] = useState(externalLoginFailed);

  useEffect(() => {
    let subscribed = true;
    if (discoveryRequest.current?.attempt !== discoveryAttempt) {
      discoveryRequest.current = {
        attempt: discoveryAttempt,
        promise: requestLoginDiscovery(window.location.origin),
      };
    }
    void discoveryRequest.current.promise.then((result) => {
      if (subscribed) {
        setDiscoveryState(result);
      }
    });
    return () => {
      subscribed = false;
    };
  }, [discoveryAttempt]);

  function retryDiscovery() {
    setDiscoveryState({ kind: "loading" });
    setDiscoveryAttempt((attempt) => attempt + 1);
  }

  return (
    <AccessPageShell
      aside={<LoginContext state={discoveryState} />}
      asideLabel={message("webapp.login.institution.label")}
      skipLabel={message("webapp.login.skip_to_main")}
      variant="split"
    >
      <section className={styles.page} aria-labelledby="login-heading">
        <header className={styles.headingGroup}>
          <h1 id="login-heading">{message("webapp.login.heading")}</h1>
          <p>{message("webapp.login.lede")}</p>
        </header>

        {externalNotice ? (
          <Notice role="note" tone="warning">
            <p>{message("webapp.login.external_failure")}</p>
          </Notice>
        ) : null}

        <DiscoveryContent
          authenticate={authenticateLocal}
          state={discoveryState}
          onRetry={retryDiscovery}
          onAuthenticationAction={() => setExternalNotice(false)}
        />
      </section>
    </AccessPageShell>
  );
}

interface DiscoveryContentProps {
  authenticate: AuthenticateLocal;
  onAuthenticationAction(): void;
  onRetry(): void;
  state: LoginDiscoveryState;
}

function DiscoveryContent({
  authenticate,
  onAuthenticationAction,
  onRetry,
  state,
}: DiscoveryContentProps) {
  if (state.kind === "loading") {
    return (
      <p className={styles.loading} role="status" aria-live="polite">
        {message("webapp.login.loading")}
      </p>
    );
  }
  if (state.kind === "setup") {
    return (
      <RouteState
        heading={message("webapp.login.setup.heading")}
        body={message("webapp.login.setup.body")}
      >
        <ButtonLink href="/setup">
          {message("webapp.login.setup.open")}
        </ButtonLink>
      </RouteState>
    );
  }
  if (state.kind === "origin_mismatch") {
    return (
      <RouteState
        heading={message("webapp.login.origin_mismatch.heading")}
        body={message("webapp.login.origin_mismatch.body")}
      >
        <Button onClick={() => window.location.reload()}>
          {message("webapp.login.reload")}
        </Button>
      </RouteState>
    );
  }
  if (state.kind === "failure" || state.kind === "unavailable") {
    const unavailable = state.kind === "unavailable";
    return (
      <RouteState
        heading={message(
          unavailable
            ? "webapp.login.unavailable.heading"
            : "webapp.login.discovery_failure.heading",
        )}
        body={message(
          unavailable
            ? "webapp.login.unavailable.body"
            : "webapp.login.discovery_failure.body",
        )}
      >
        <Button onClick={onRetry}>
          {message("webapp.login.retry")}
        </Button>
      </RouteState>
    );
  }
  return (
    <LoginMethods
      authenticate={authenticate}
      discovery={state.discovery}
      onAuthenticationAction={onAuthenticationAction}
    />
  );
}

function RouteState({
  body,
  children,
  heading,
}: {
  body: string;
  children: React.ReactNode;
  heading: string;
}) {
  return (
    <div className={styles.routeState}>
      <div className={styles.routeStateCopy}>
        <h2>{heading}</h2>
        <p>{body}</p>
      </div>
      {children}
    </div>
  );
}

function LoginMethods({
  authenticate,
  discovery,
  onAuthenticationAction,
}: {
  authenticate: AuthenticateLocal;
  discovery: PublicAccessDiscovery;
  onAuthenticationAction(): void;
}) {
  const hasLocal = discovery.capabilities.local_login;
  const hasProviders = discovery.providers.length > 0;

  return (
    <div className={styles.methods}>
      {hasLocal ? (
        <LocalLoginForm
          authenticate={authenticate}
          onAuthenticationAction={onAuthenticationAction}
        />
      ) : null}

      {hasLocal && hasProviders ? (
        <div className={styles.separator} aria-hidden="true">
          <span>{message("webapp.login.method_separator")}</span>
        </div>
      ) : null}

      {hasProviders ? (
        <fieldset className={styles.providerGroup}>
          <legend className={styles.visuallyHidden}>
            {message("webapp.login.provider.group")}
          </legend>
          {discovery.providers.map((provider) => (
            <Button
              key={provider.id}
              variant="secondary"
              onClick={() => {
                onAuthenticationAction();
                const parameters = new URLSearchParams({
                  client_type: "web",
                  return_to: "/authorization/complete",
                });
                navigateToProvider(
                  `/api/v1/auth/providers/${encodeURIComponent(provider.id)}/login?${parameters.toString()}`,
                );
              }}
            >
              {message("webapp.login.provider.continue", {
                Provider: provider.display_name,
              })}
            </Button>
          ))}
        </fieldset>
      ) : null}

      {discovery.capabilities.public_registration ? (
        <div className={styles.registration}>
          <p>{message("webapp.login.registration.prompt")}</p>
          <a href="/register">{message("webapp.login.registration.action")}</a>
        </div>
      ) : null}
    </div>
  );
}

function LocalLoginForm({
  authenticate,
  onAuthenticationAction,
}: {
  authenticate: AuthenticateLocal;
  onAuthenticationAction(): void;
}) {
  const [loginID, setLoginID] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMFACode] = useState("");
  const [mfaRequired, setMFARequired] = useState(false);
  const [pending, setPending] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string>();
  const [liveMessage, setLiveMessage] = useState("");
  const loginIDRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const mfaCodeRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (mfaRequired) {
      mfaCodeRef.current?.focus();
    }
  }, [mfaRequired]);

  function clearFormFailure() {
    setFormError(undefined);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) {
      return;
    }
    onAuthenticationAction();

    const nextErrors: FieldErrors = {};
    if (loginID.trim() === "") {
      nextErrors.login_id = message("webapp.login.form.error.username_required");
    }
    if (password === "") {
      nextErrors.password = message("webapp.login.form.error.password_required");
    }
    if (mfaRequired && mfaCode.trim() === "") {
      nextErrors.mfa_code = message("webapp.login.form.error.mfa_invalid");
    }
    setFieldErrors(nextErrors);
    setFormError(undefined);
    if (nextErrors.login_id !== undefined) {
      loginIDRef.current?.focus();
      return;
    }
    if (nextErrors.password !== undefined) {
      passwordRef.current?.focus();
      return;
    }
    if (nextErrors.mfa_code !== undefined) {
      mfaCodeRef.current?.focus();
      return;
    }

    setPending(true);
    setLiveMessage(message("webapp.login.form.signing_in"));
    try {
      const result = await authenticate({
        loginID,
        password,
        ...(mfaRequired ? { mfaCode } : {}),
      });
      if (result.kind === "authenticated") {
        setLoginID("");
        setPassword("");
        setMFACode("");
        window.location.replace("/authorization/complete");
        return;
      }

      if (result.kind === "mfa_required") {
        setMFARequired(true);
        setMFACode("");
        setFieldErrors({});
        setFormError(undefined);
        setLiveMessage(message("webapp.login.form.mfa_help"));
        return;
      }
      if (result.kind === "mfa_invalid") {
        const errorMessage = message("webapp.login.form.error.mfa_invalid");
        setFieldErrors({ mfa_code: errorMessage });
        setLiveMessage(errorMessage);
        requestAnimationFrame(() => mfaCodeRef.current?.focus());
        return;
      }

      let safeError: string;
      switch (result.kind) {
        case "invalid_credentials":
          safeError = message("webapp.login.form.error.invalid_credentials");
          break;
        case "mfa_unavailable":
          safeError = message("webapp.login.form.error.mfa_unavailable");
          break;
        case "session_limit":
          safeError = message("webapp.login.form.error.sessions_maximum");
          break;
        case "rate_limited":
          safeError = message("webapp.login.form.error.rate_limited");
          break;
        default:
          safeError = message("webapp.login.form.error.generic");
      }
      setFormError(safeError);
      setLiveMessage("");
    } catch {
      const safeError = message("webapp.login.form.error.generic");
      setFormError(safeError);
      setLiveMessage("");
    } finally {
      setPending(false);
    }
  }

  return (
    <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
      <div className={styles.visuallyHidden} aria-live="polite" aria-atomic="true">
        {liveMessage}
      </div>

      <InputField
        ref={loginIDRef}
        id="login-id"
        name="login_id"
        label={message("webapp.login.form.email_or_username")}
        type="text"
        autoCapitalize="none"
        autoComplete="username"
        spellCheck={false}
        value={loginID}
        errorMessage={fieldErrors.login_id}
        required
        onChange={(event) => {
          setLoginID(event.currentTarget.value);
          setFieldErrors((errors) => ({ ...errors, login_id: undefined }));
          clearFormFailure();
        }}
      />

      <PasswordField
        ref={passwordRef}
        id="password"
        name="password"
        label={message("webapp.login.form.password")}
        labelAccessory={
          <a href="/account/forgot-password">
            {message("webapp.login.form.forgot_password")}
          </a>
        }
        autoComplete="current-password"
        value={password}
        errorMessage={fieldErrors.password}
        hidePasswordLabel={message("webapp.form.password_hide")}
        showPasswordLabel={message("webapp.form.password_show")}
        toggleDisabled={pending}
        required
        onChange={(event) => {
          setPassword(event.currentTarget.value);
          setFieldErrors((errors) => ({ ...errors, password: undefined }));
          clearFormFailure();
        }}
      />

      {mfaRequired ? (
        <div className={styles.mfaGroup}>
          <div className={styles.mfaHeading}>
            <h2>{message("webapp.login.form.mfa_heading")}</h2>
            <p id="mfa-help">{message("webapp.login.form.mfa_help")}</p>
          </div>
          <InputField
            ref={mfaCodeRef}
            id="mfa-code"
            name="mfa_code"
            label={message("webapp.login.form.mfa_code")}
            type="text"
            autoCapitalize="none"
            autoComplete="one-time-code"
            describedBy="mfa-help"
            spellCheck={false}
            value={mfaCode}
            errorMessage={fieldErrors.mfa_code}
            required
            onChange={(event) => {
              setMFACode(event.currentTarget.value);
              setFieldErrors((errors) => ({ ...errors, mfa_code: undefined }));
              clearFormFailure();
            }}
          />
          <Button
            disabled={pending}
            variant="text"
            onClick={() => {
              setMFARequired(false);
              setPassword("");
              setMFACode("");
              setFieldErrors({});
              setFormError(undefined);
              setLiveMessage("");
              requestAnimationFrame(() => passwordRef.current?.focus());
            }}
          >
            {message("webapp.login.form.mfa_back")}
          </Button>
        </div>
      ) : null}

      <div className={styles.submission}>
        <Button
          type="submit"
          isLoading={pending}
          loadingLabel={message("webapp.login.form.signing_in")}
        >
          {message("webapp.login.form.sign_in")}
        </Button>
        <FormFeedback message={formError} />
      </div>
    </form>
  );
}
