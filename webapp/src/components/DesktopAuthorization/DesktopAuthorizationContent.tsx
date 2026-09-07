import { type FormEvent, useEffect, useRef, useState } from "react";

import type { DesktopAuthorizationContext, DesktopAuthorizationProvider, DesktopLocalAuthenticationSubmission } from "../../features/desktop-authorization/DesktopAuthorizationApi";
import type { DesktopAuthorizationJourney, DesktopJourneySnapshot, DesktopLocalFeedback, DesktopTerminalState } from "../../features/desktop-authorization/DesktopAuthorizationJourney";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { AuthenticationCodeField, isCompleteAuthenticationCode, type AuthenticationCodeValue } from "../AuthenticationCodeField/AuthenticationCodeField";
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

export interface DesktopAuthorizationContentProps {
  journey: DesktopAuthorizationJourney;
}

export function DesktopAuthorizationContent({ journey }: DesktopAuthorizationContentProps) {
  const { view, pending, focusHeading } = journey.snapshot;
  const feedback = journey.snapshot.feedback === "unavailable"
    ? message("webapp.desktop_authorization.error.unavailable") : undefined;
  const effectiveTerminal = view.kind === "approved" || view.kind === "cancelled" ||
    view.kind === "invalid" || view.kind === "locked" ? view.kind : undefined;
  if (effectiveTerminal !== undefined) {
    const heading = terminalCopy(effectiveTerminal).heading;
    return (
      <>
        <TaskStateAnnouncement
          message={focusHeading ? heading : ""}
        />
        <TerminalContent
          focusHeading={focusHeading}
          state={effectiveTerminal}
        />
      </>
    );
  }
  if (view.kind === "checking") {
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
  if (view.kind === "unavailable") {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.desktop_authorization.unavailable.heading")}
        />
        <TaskState
          body={message("webapp.desktop_authorization.unavailable.body")}
          className={styles.taskState}
          focusHeading={focusHeading}
          heading={message("webapp.desktop_authorization.unavailable.heading")}
          headingID="desktop-heading"
          label={message("webapp.desktop_authorization.label")}
        >
          <TaskStateActions>
            <Button disabled={pending !== undefined} onClick={journey.retry}>
              {message("webapp.desktop_authorization.unavailable.retry")}
            </Button>
          </TaskStateActions>
        </TaskState>
      </>
    );
  }
  if (view.kind === "authentication") {
    return (
      <AuthenticationContent
        authenticate={journey.authenticate}
        cancel={journey.cancel}
        context={view.context}
        onProvider={(provider) => journey.chooseProvider(provider.id)}
        pending={pending}
        feedback={feedback}
        focusHeading={focusHeading}
      />
    );
  }
  if (view.kind !== "confirmation") return null;

  if (view.context.account === undefined) {
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
          focusHeading={focusHeading}
          heading={message("webapp.desktop_authorization.heading")}
          body={message("webapp.desktop_authorization.lede")}
          headingID="desktop-heading"
        />
        <AuthorizationDetails
          account={view.context.account}
          context={view.context}
        />
        <Notice role="note" tone="warning">
          {message("webapp.desktop_authorization.caution")}
        </Notice>
        <TaskStateActions>
          <Button
            disabled={pending !== undefined && pending !== "approve"}
            isLoading={pending === "approve"}
            loadingLabel={message("webapp.desktop_authorization.approving")}
            onClick={journey.approve}
          >
            {message("webapp.desktop_authorization.approve")}
          </Button>
          <Button
            disabled={pending === "approve" || pending === "cancel"}
            isLoading={pending === "reset"}
            loadingLabel={message("webapp.desktop_authorization.resetting")}
            variant="secondary"
            onClick={journey.useAnotherAccount}
          >
            {message("webapp.desktop_authorization.use_another_account")}
          </Button>
          <Button
            disabled={pending === "approve" || pending === "reset"}
            isLoading={pending === "cancel"}
            loadingLabel={message("webapp.desktop_authorization.cancelling")}
            variant="text"
            onClick={journey.cancel}
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
  authenticate, cancel, context, feedback, onProvider, pending, focusHeading,
}: {
  authenticate(submission: DesktopLocalAuthenticationSubmission): Promise<DesktopLocalFeedback>;
  cancel(): void;
  context: DesktopAuthorizationContext;
  feedback?: string;
  onProvider(provider: DesktopAuthorizationProvider): void;
  pending: DesktopJourneySnapshot["pending"];
  focusHeading: boolean;
}) {
  return (
    <>
      <TaskStateAnnouncement
        message={message("webapp.desktop_authorization.authentication.heading")}
      />
      <section className={styles.page} aria-labelledby="desktop-heading">
        <AccessTaskIntro
          eyebrow={message("webapp.desktop_authorization.label")}
          focusHeading={focusHeading}
          heading={message("webapp.desktop_authorization.authentication.heading")}
          body={message("webapp.desktop_authorization.authentication.body")}
          headingID="desktop-heading"
        />
        <p className={styles.installation}>{context.installation}</p>
        {context.localLoginEnabled ? (
          <DesktopLocalLoginForm
            authenticate={authenticate}
            busy={pending !== undefined}
            authenticating={pending === "authenticate"}
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
                disabled={pending !== undefined}
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
          disabled={pending !== undefined && pending !== "cancel"}
          isLoading={pending === "cancel"}
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

function DesktopLocalLoginForm({ authenticate, busy, authenticating }: {
  authenticate(submission: DesktopLocalAuthenticationSubmission): Promise<DesktopLocalFeedback>;
  busy: boolean;
  authenticating: boolean;
}) {
  const [loginID, setLoginID] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMFACode] = useState<AuthenticationCodeValue>({ kind: "authenticator", code: "" });
  const [mfaRequired, setMFARequired] = useState(false);
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
    if (busy) {
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
    if (mfaRequired && !isCompleteAuthenticationCode(mfaCode)) {
      setFormError(message("webapp.login.form.error.mfa_invalid"));
      mfaCodeRef.current?.focus();
      return;
    }

    setFormError(undefined);
    const result = await authenticate({
      loginID,
      password,
      ...(mfaRequired ? { mfaCode: mfaCode.code } : {}),
    });
    if (result === "authenticated") {
      setPassword("");
      setMFACode({ kind: "authenticator", code: "" });
      return;
    }
    if (result === "mfa_required") {
      setMFARequired(true);
      setMFACode({ kind: "authenticator", code: "" });
      return;
    }
    if (result === "mfa_invalid") {
      setFormError(message("webapp.login.form.error.mfa_invalid"));
      requestAnimationFrame(() => mfaCodeRef.current?.focus());
      return;
    }
    if (result === "invalid_credentials") {
      setFormError(message("webapp.login.form.error.invalid_credentials"));
      return;
    }
    if (result === "rate_limited") {
      setFormError(message("webapp.login.form.error.rate_limited"));
      return;
    }
  }

  return (
    <form className={styles.form} onSubmit={submit} aria-busy={busy} noValidate>
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
        toggleDisabled={busy}
        required
        onChange={(event) => {
          setPassword(event.currentTarget.value);
          setFormError(undefined);
        }}
      />
      {mfaRequired ? (
        <AuthenticationCodeField
          ref={mfaCodeRef}
          id="desktop-mfa-code"
          name="mfa_code"
          value={mfaCode}
          disabled={busy}
          onChange={(value) => {
            setMFACode(value);
            setFormError(undefined);
          }}
        />
      ) : null}
      <Button
        disabled={busy}
        isLoading={authenticating}
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
  state: DesktopTerminalState;
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

function terminalCopy(state: DesktopTerminalState) {
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
