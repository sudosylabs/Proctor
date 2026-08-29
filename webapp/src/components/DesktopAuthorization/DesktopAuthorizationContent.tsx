import { type FormEvent, useEffect, useRef, useState } from "react";

import type {
  DesktopAccountResetResult,
  DesktopApprovalResult,
  DesktopAuthenticationResult,
  DesktopAuthorizationContext,
  DesktopAuthorizationProvider,
  DesktopCancellationResult,
  DesktopContextResult,
  DesktopLocalAuthenticationSubmission,
} from "../../features/desktop-authorization/DesktopAuthorizationApi";
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
import styles from "./DesktopAuthorization.module.css";

type TerminalState = "approved" | "cancelled" | "invalid" | "locked";

export interface DesktopAuthorizationContentProps {
  approve(state: string): Promise<DesktopApprovalResult>;
  authenticateLocal(
    installation: string,
    submission: DesktopLocalAuthenticationSubmission,
  ): Promise<DesktopAuthenticationResult>;
  cancel(state: string): Promise<DesktopCancellationResult>;
  checking: boolean;
  context: DesktopContextResult;
  onApproved(redirectURL: string): void;
  onContextChange(context: DesktopContextResult): void;
  onProvider(url: string): void;
  onRetryContext(): void;
  providerURL(providerID: string, state: string): string;
  reloadContext(servingOrigin: string): Promise<DesktopContextResult>;
  resetAccount(): Promise<DesktopAccountResetResult>;
  state?: string;
}

export function DesktopAuthorizationContent({
  approve,
  authenticateLocal,
  cancel,
  checking,
  context,
  onApproved,
  onContextChange,
  onProvider,
  onRetryContext,
  providerURL,
  reloadContext,
  resetAccount,
  state,
}: DesktopAuthorizationContentProps) {
  const [terminal, setTerminal] = useState<TerminalState>();
  const [pending, setPending] = useState<"approve" | "cancel" | "reset">();
  const [feedback, setFeedback] = useState<string>();
  const [retryRequested, setRetryRequested] = useState(false);
  const [transitionTriggered, setTransitionTriggered] = useState(false);

  const effectiveTerminal =
    terminal ??
    (context.kind === "invalid" || context.kind === "locked"
      ? context.kind
      : undefined);

  function moveToFailure(result: { kind: string }): boolean {
    if (result.kind === "invalid" || result.kind === "locked") {
      setTransitionTriggered(true);
      setTerminal(result.kind);
      return true;
    }
    return false;
  }

  async function approveRequest() {
    if (state === undefined || pending !== undefined) {
      return;
    }
    setPending("approve");
    setFeedback(undefined);
    const result = await approve(state);
    if (result.kind === "approved") {
      setTransitionTriggered(true);
      setTerminal("approved");
      onApproved(result.redirectURL);
      return;
    }
    if (!moveToFailure(result)) {
      setFeedback(message("webapp.desktop_authorization.error.unavailable"));
    }
    setPending(undefined);
  }

  async function cancelRequest() {
    if (state === undefined || pending !== undefined) {
      return;
    }
    setPending("cancel");
    setFeedback(undefined);
    const result = await cancel(state);
    if (result.kind === "cancelled") {
      setTransitionTriggered(true);
      setTerminal("cancelled");
    } else if (!moveToFailure(result)) {
      setFeedback(message("webapp.desktop_authorization.error.unavailable"));
    }
    setPending(undefined);
  }

  async function useAnotherAccount() {
    if (pending !== undefined) {
      return;
    }
    setPending("reset");
    setFeedback(undefined);
    const result = await resetAccount();
    if (result.kind !== "reset") {
      if (!moveToFailure(result)) {
        setFeedback(message("webapp.desktop_authorization.error.unavailable"));
      }
      setPending(undefined);
      return;
    }
    const loaded = await reloadContext(window.location.origin);
    if (loaded.kind === "ready") {
      onContextChange(loaded);
    } else if (!moveToFailure(loaded)) {
      setFeedback(message("webapp.desktop_authorization.error.unavailable"));
    }
    setPending(undefined);
  }

  function retryContext() {
    setRetryRequested(true);
    onRetryContext();
  }

  if (effectiveTerminal !== undefined) {
    const heading = terminalCopy(effectiveTerminal).heading;
    return (
      <>
        <TaskStateAnnouncement
          message={transitionTriggered ? heading : ""}
        />
        <TerminalContent
          focusHeading={transitionTriggered}
          state={effectiveTerminal}
        />
      </>
    );
  }
  if (checking) {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.desktop_authorization.checking.heading")}
        />
        <TaskState
          body={message("webapp.desktop_authorization.checking.body")}
          busy
          className={styles.taskState}
          heading={message("webapp.desktop_authorization.checking.heading")}
          headingID="desktop-heading"
          label={message("webapp.desktop_authorization.label")}
        />
      </>
    );
  }
  if (context.kind === "unavailable") {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.desktop_authorization.unavailable.heading")}
        />
        <TaskState
          body={message("webapp.desktop_authorization.unavailable.body")}
          className={styles.taskState}
          focusHeading={retryRequested}
          heading={message("webapp.desktop_authorization.unavailable.heading")}
          headingID="desktop-heading"
          label={message("webapp.desktop_authorization.label")}
        >
          <TaskStateActions>
            <Button onClick={retryContext}>
              {message("webapp.desktop_authorization.unavailable.retry")}
            </Button>
          </TaskStateActions>
        </TaskState>
      </>
    );
  }
  if (context.kind !== "ready" || state === undefined) {
    return null;
  }

  if (context.context.state === "bound") {
    return (
      <AuthenticationContent
        authenticate={authenticateLocal}
        cancel={cancelRequest}
        context={context.context}
        onAuthenticated={(authenticated) =>
          onContextChange({ kind: "ready", context: authenticated })
        }
        onFailure={(result) => {
          if (!moveToFailure(result)) {
            setFeedback(message("webapp.desktop_authorization.error.unavailable"));
          }
        }}
        onProvider={(provider) => onProvider(providerURL(provider.id, state))}
        pending={pending === "cancel"}
        feedback={feedback}
      />
    );
  }

  if (context.context.account === undefined) {
    return null;
  }
  return (
    <>
      <TaskStateAnnouncement
        message={message("webapp.desktop_authorization.heading")}
      />
      <section className={styles.page} aria-labelledby="desktop-heading">
        <AccessTaskIntro
          eyebrow={message("webapp.desktop_authorization.label")}
          focusHeading={retryRequested}
          heading={message("webapp.desktop_authorization.heading")}
          body={message("webapp.desktop_authorization.lede")}
          headingID="desktop-heading"
        />
        <AuthorizationDetails
          account={context.context.account}
          context={context.context}
        />
        <Notice role="note" tone="warning">
          {message("webapp.desktop_authorization.caution")}
        </Notice>
        <TaskStateActions>
          <Button
            isLoading={pending === "approve"}
            loadingLabel={message("webapp.desktop_authorization.approving")}
            onClick={approveRequest}
          >
            {message("webapp.desktop_authorization.approve")}
          </Button>
          <Button
            disabled={pending === "approve" || pending === "cancel"}
            isLoading={pending === "reset"}
            loadingLabel={message("webapp.desktop_authorization.resetting")}
            variant="secondary"
            onClick={useAnotherAccount}
          >
            {message("webapp.desktop_authorization.use_another_account")}
          </Button>
          <Button
            disabled={pending === "approve" || pending === "reset"}
            isLoading={pending === "cancel"}
            loadingLabel={message("webapp.desktop_authorization.cancelling")}
            variant="text"
            onClick={cancelRequest}
          >
            {message("webapp.desktop_authorization.cancel")}
          </Button>
        </TaskStateActions>
        <p className={styles.expiry}>
          {message("webapp.desktop_authorization.expiry")}
        </p>
        <FormFeedback message={feedback} />
      </section>
    </>
  );
}

function AuthenticationContent({
  authenticate,
  cancel,
  context,
  feedback,
  onAuthenticated,
  onFailure,
  onProvider,
  pending,
}: {
  authenticate(
    installation: string,
    submission: DesktopLocalAuthenticationSubmission,
  ): Promise<DesktopAuthenticationResult>;
  cancel(): void;
  context: DesktopAuthorizationContext;
  feedback?: string;
  onAuthenticated(context: DesktopAuthorizationContext): void;
  onFailure(result: DesktopAuthenticationResult): void;
  onProvider(provider: DesktopAuthorizationProvider): void;
  pending: boolean;
}) {
  return (
    <>
      <TaskStateAnnouncement
        message={message("webapp.desktop_authorization.authentication.heading")}
      />
      <section className={styles.page} aria-labelledby="desktop-heading">
        <AccessTaskIntro
          eyebrow={message("webapp.desktop_authorization.label")}
          heading={message("webapp.desktop_authorization.authentication.heading")}
          body={message("webapp.desktop_authorization.authentication.body")}
          headingID="desktop-heading"
        />
        <p className={styles.installation}>{context.installation}</p>
        {context.localLoginEnabled ? (
          <DesktopLocalLoginForm
            authenticate={(submission) =>
              authenticate(context.installation, submission)
            }
            onAuthenticated={onAuthenticated}
            onFailure={onFailure}
          />
        ) : null}
        {context.localLoginEnabled && context.externalProviders.length > 0 ? (
          <div className={styles.separator} aria-hidden="true">
            <span>{message("webapp.login.method_separator")}</span>
          </div>
        ) : null}
        {context.externalProviders.length > 0 ? (
          <fieldset className={styles.providerGroup}>
            <legend className={styles.visuallyHidden}>
              {message("webapp.desktop_authorization.provider.group")}
            </legend>
            {context.externalProviders.map((provider) => (
              <Button
                key={provider.id}
                variant="secondary"
                onClick={() => onProvider(provider)}
              >
                {message("webapp.desktop_authorization.provider.continue", {
                  Provider: provider.display_name,
                })}
              </Button>
            ))}
          </fieldset>
        ) : null}
        <Button
          isLoading={pending}
          loadingLabel={message("webapp.desktop_authorization.cancelling")}
          variant="text"
          onClick={cancel}
        >
          {message("webapp.desktop_authorization.cancel")}
        </Button>
        <FormFeedback message={feedback} />
      </section>
    </>
  );
}

function DesktopLocalLoginForm({
  authenticate,
  onAuthenticated,
  onFailure,
}: {
  authenticate(
    submission: DesktopLocalAuthenticationSubmission,
  ): Promise<DesktopAuthenticationResult>;
  onAuthenticated(context: DesktopAuthorizationContext): void;
  onFailure(result: DesktopAuthenticationResult): void;
}) {
  const [loginID, setLoginID] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMFACode] = useState("");
  const [mfaRequired, setMFARequired] = useState(false);
  const [pending, setPending] = useState(false);
  const [formError, setFormError] = useState<string>();
  const loginIDRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const mfaCodeRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (mfaRequired) {
      mfaCodeRef.current?.focus();
    }
  }, [mfaRequired]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) {
      return;
    }
    if (loginID.trim() === "") {
      setFormError(message("webapp.login.form.error.username_required"));
      loginIDRef.current?.focus();
      return;
    }
    if (password === "") {
      setFormError(message("webapp.login.form.error.password_required"));
      passwordRef.current?.focus();
      return;
    }
    if (mfaRequired && mfaCode.trim() === "") {
      setFormError(message("webapp.login.form.error.mfa_invalid"));
      mfaCodeRef.current?.focus();
      return;
    }

    setPending(true);
    setFormError(undefined);
    const result = await authenticate({
      loginID,
      password,
      ...(mfaRequired ? { mfaCode } : {}),
    });
    setPending(false);
    if (result.kind === "authenticated") {
      setPassword("");
      setMFACode("");
      onAuthenticated(result.context);
      return;
    }
    if (result.kind === "mfa_required") {
      setMFARequired(true);
      setMFACode("");
      return;
    }
    if (result.kind === "mfa_invalid") {
      setFormError(message("webapp.login.form.error.mfa_invalid"));
      requestAnimationFrame(() => mfaCodeRef.current?.focus());
      return;
    }
    if (result.kind === "invalid_credentials") {
      setFormError(message("webapp.login.form.error.invalid_credentials"));
      return;
    }
    if (result.kind === "rate_limited") {
      setFormError(message("webapp.login.form.error.rate_limited"));
      return;
    }
    onFailure(result);
  }

  return (
    <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
      <InputField
        ref={loginIDRef}
        id="desktop-login-id"
        name="login_id"
        label={message("webapp.login.form.email_or_username")}
        type="text"
        autoCapitalize="none"
        autoComplete="username"
        spellCheck={false}
        value={loginID}
        required
        onChange={(event) => {
          setLoginID(event.currentTarget.value);
          setFormError(undefined);
        }}
      />
      <PasswordField
        ref={passwordRef}
        id="desktop-password"
        name="password"
        label={message("webapp.login.form.password")}
        autoComplete="current-password"
        value={password}
        hidePasswordLabel={message("webapp.form.password_hide")}
        showPasswordLabel={message("webapp.form.password_show")}
        toggleDisabled={pending}
        required
        onChange={(event) => {
          setPassword(event.currentTarget.value);
          setFormError(undefined);
        }}
      />
      {mfaRequired ? (
        <InputField
          ref={mfaCodeRef}
          id="desktop-mfa-code"
          name="mfa_code"
          label={message("webapp.login.form.mfa_code")}
          type="text"
          autoComplete="one-time-code"
          inputMode="numeric"
          value={mfaCode}
          required
          onChange={(event) => {
            setMFACode(event.currentTarget.value);
            setFormError(undefined);
          }}
        />
      ) : null}
      <Button
        isLoading={pending}
        loadingLabel={message("webapp.login.form.signing_in")}
        type="submit"
      >
        {message("webapp.login.form.sign_in")}
      </Button>
      <FormFeedback message={formError} />
    </form>
  );
}

function AuthorizationDetails({
  account,
  context,
}: {
  account: NonNullable<DesktopAuthorizationContext["account"]>;
  context: DesktopAuthorizationContext;
}) {
  return (
    <div className={styles.details}>
      <h2>{message("webapp.desktop_authorization.details")}</h2>
      <dl>
        <div>
          <dt>{message("webapp.desktop_authorization.installation")}</dt>
          <dd>{context.installation}</dd>
        </div>
        <div>
          <dt>{message("webapp.desktop_authorization.account")}</dt>
          <dd translate="no">{account.username}</dd>
        </div>
        <div>
          <dt>{message("webapp.desktop_authorization.request")}</dt>
          <dd>{context.deviceName || message("webapp.desktop_authorization.request_value")}</dd>
        </div>
      </dl>
    </div>
  );
}

function TerminalContent({
  focusHeading,
  state,
}: {
  focusHeading: boolean;
  state: TerminalState;
}) {
  const content = terminalCopy(state);
  return (
    <TaskState
      body={content.body}
      className={styles.taskState}
      focusHeading={focusHeading}
      heading={content.heading}
      headingID="desktop-heading"
      label={content.label}
    >
      {state === "invalid" ? (
        <TaskStateActions>
          <ButtonLink href="/login" variant="secondary">
            {message("webapp.desktop_authorization.return_to_sign_in")}
          </ButtonLink>
        </TaskStateActions>
      ) : null}
    </TaskState>
  );
}

function terminalCopy(state: TerminalState) {
  switch (state) {
    case "approved":
      return {
        label: message("webapp.desktop_authorization.approved.label"),
        heading: message("webapp.desktop_authorization.approved.heading"),
        body: message("webapp.desktop_authorization.approved.body"),
      };
    case "cancelled":
      return {
        label: message("webapp.desktop_authorization.cancelled.label"),
        heading: message("webapp.desktop_authorization.cancelled.heading"),
        body: message("webapp.desktop_authorization.cancelled.body"),
      };
    case "locked":
      return {
        label: message("webapp.desktop_authorization.locked.label"),
        heading: message("webapp.desktop_authorization.locked.heading"),
        body: message("webapp.desktop_authorization.locked.body"),
      };
    case "invalid":
      return {
        label: message("webapp.desktop_authorization.invalid.label"),
        heading: message("webapp.desktop_authorization.invalid.heading"),
        body: message("webapp.desktop_authorization.invalid.body"),
      };
  }
}
