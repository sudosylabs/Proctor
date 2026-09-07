import { useEffect, useRef, useState, type FormEvent } from "react";

import type { SecurityContextResult } from "../../features/account-security/AccountSecurityApi";
import { reauthenticationDestination, type ReauthenticationActions, type ReauthenticationFailure, type ReauthenticationTask } from "../../features/reauthenticate/ReauthenticationApi";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { SecurityCodeForm } from "../AccountSecurity/SecurityCodeForm";
import { securityFailureMessage } from "../AccountSecurity/SecurityFeedback";
import { Button, ButtonLink } from "../Button/Button";
import { FormFeedback } from "../FormFeedback/FormFeedback";
import { PasswordField } from "../InputField/PasswordField";
import { Notice } from "../Notice/Notice";
import { TaskState, TaskStateActions, TaskStateAnnouncement } from "../TaskState/TaskState";
import styles from "./Reauthentication.module.css";

export function ReauthenticationContent({ actions, externalProofFailed, loading, onRefresh, state, task }: {
  actions: ReauthenticationActions;
  externalProofFailed: boolean;
  loading: boolean;
  onRefresh(): void;
  state: SecurityContextResult;
  task: ReauthenticationTask;
}) {
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<ReauthenticationFailure>();
  const [focusHeading, setFocusHeading] = useState(false);
  const [showExternalFailure, setShowExternalFailure] = useState(externalProofFailed);
  const [formGeneration, setFormGeneration] = useState(0);
  const viewGeneration = useRef(0);

  useEffect(() => {
    function discardSensitiveView() {
      viewGeneration.current += 1;
      setFormGeneration((current) => current + 1); setPending(false); setFailure(undefined);
    }
    function restore(event: PageTransitionEvent) {
      if (event.persisted) { discardSensitiveView(); onRefresh(); }
    }
    window.addEventListener("pagehide", discardSensitiveView);
    window.addEventListener("pageshow", restore);
    return () => {
      viewGeneration.current += 1;
      window.removeEventListener("pagehide", discardSensitiveView);
      window.removeEventListener("pageshow", restore);
    };
  }, [onRefresh]);

  function refresh() {
    setFailure(undefined); setFocusHeading(true); onRefresh();
  }

  function starting() { setPending(true); setFailure(undefined); setShowExternalFailure(false); }

  async function passwordProof(password: string) {
    if (pending) return;
    starting();
    const generation = viewGeneration.current;
    const result = await actions.password(password);
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success") refresh(); else setFailure(result);
    setPending(false);
  }

  async function externalProof() {
    if (pending) return;
    starting();
    const generation = viewGeneration.current;
    const result = await actions.external(task);
    if (generation !== viewGeneration.current) return;
    if (result.kind === "redirect") { window.location.assign(result.url); return; }
    setFailure(result); setPending(false);
  }

  async function challenge(code: string) {
    if (pending) return;
    starting();
    const generation = viewGeneration.current;
    const result = await actions.challenge(code);
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success") refresh(); else setFailure(result);
    setPending(false);
  }

  if (loading || state.kind !== "ready") {
    const noSession = !loading && state.kind === "no_session";
    const heading = message(loading ? "webapp.security.loading.heading" : noSession ? "webapp.security.no_session.heading" : "webapp.security.unavailable.heading");
    return <>
      <TaskStateAnnouncement message={heading} />
      <TaskState heading={heading} headingID="reauthentication-heading" focusHeading={focusHeading} busy={loading}
        body={message(loading ? "webapp.security.loading.body" : noSession ? "webapp.security.no_session.body" : "webapp.security.unavailable.body")}>
        {!loading ? <TaskStateActions>{noSession
          ? <ButtonLink href="/login">{message("webapp.security.sign_in")}</ButtonLink>
          : <Button onClick={refresh}>{message("webapp.security.refresh")}</Button>}
        </TaskStateActions> : null}
      </TaskState>
    </>;
  }

  const context = state.context;
  const requiresRecovery = context.recoveryRequired && task === "connect-provider";
  const needsPrimary = !context.recentlyAuthenticated;
  const needsStrong = !context.recoveryRequired && context.authenticationStrength !== "multi_factor" && (context.enabled || task === "connect-provider");
  const complete = !requiresRecovery && !needsPrimary && !needsStrong;
  const step = requiresRecovery ? "recovery" : needsPrimary ? "primary" : needsStrong ? "strong" : "complete";

  return (
    <section className={styles.page} aria-labelledby="reauthentication-heading">
      <TaskStateAnnouncement message={pending ? message("webapp.security.working") : ""} />
      <AccessTaskIntro key={step} headingID="reauthentication-heading" focusHeading={focusHeading}
        eyebrow={message("webapp.security.eyebrow")}
        heading={message(complete ? "webapp.reauthenticate.complete.heading" : "webapp.reauthenticate.heading")}
        body={message(complete ? "webapp.reauthenticate.complete.body" : "webapp.reauthenticate.body")} />
      {showExternalFailure ? <Notice tone="warning">{message("webapp.reauthenticate.external.failed")}</Notice> : null}
      {requiresRecovery ? <>
        <Notice tone="warning">{message("webapp.security.recovery_required")}</Notice>
        <ButtonLink href="/account/security">{message("webapp.security.restore_access")}</ButtonLink>
      </> : needsPrimary ? <>
        {context.authenticationMethod === "password" ? <PasswordProofForm key={formGeneration} pending={pending} onSubmit={passwordProof} /> : <>
          <Notice>{message("webapp.reauthenticate.external.help")}</Notice>
          <Button isLoading={pending} loadingLabel={message("webapp.security.working")} onClick={externalProof}>{message("webapp.reauthenticate.external.action")}</Button>
        </>}
      </> : needsStrong ? <>
        {context.enabled && context.serviceEnabled ? <>
          <Notice>{message("webapp.reauthenticate.challenge.help")}</Notice>
          <SecurityCodeForm key={formGeneration} pending={pending} onSubmit={challenge} />
        </> : <>
          <Notice tone="warning">{message(context.serviceEnabled ? "webapp.reauthenticate.setup_required" : "webapp.security.service_disabled")}</Notice>
          <ButtonLink href="/account/security">{message("webapp.security.heading")}</ButtonLink>
        </>}
      </> : <ButtonLink href={reauthenticationDestination(task)}>{message("webapp.reauthenticate.continue")}</ButtonLink>}
      <FormFeedback message={failure === undefined ? undefined : proofFailureMessage(failure)} />
      {failure !== undefined && ["method_required", "primary_required", "strong_required", "conflict", "disabled"].includes(failure.kind)
        ? <Button variant="secondary" onClick={refresh}>{message("webapp.security.refresh")}</Button> : null}
      {failure?.kind === "no_session" ? <ButtonLink href="/login">{message("webapp.security.sign_in")}</ButtonLink> : null}
      {!complete && !requiresRecovery ? <a className={styles.returnLink} href={reauthenticationDestination(task)}>{message("webapp.reauthenticate.cancel")}</a> : null}
    </section>
  );
}

function PasswordProofForm({ onSubmit, pending }: { onSubmit(password: string): Promise<void>; pending: boolean }) {
  const [password, setPassword] = useState("");
  const [missing, setMissing] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    if (password === "") { setMissing(true); input.current?.focus(); return; }
    const submitted = password; setPassword(""); await onSubmit(submitted);
  }
  return <form className={styles.form} onSubmit={submit} aria-busy={pending} noValidate>
    <PasswordField ref={input} id="current-password" name="password" label={message("webapp.reauthenticate.password.label")}
      required disabled={pending} autoComplete="current-password" value={password}
      hidePasswordLabel={message("webapp.form.password_hide")} showPasswordLabel={message("webapp.form.password_show")}
      errorMessage={missing ? message("webapp.reauthenticate.password.required") : undefined}
      onChange={(event) => { setPassword(event.currentTarget.value); setMissing(false); }} />
    <Button type="submit" isLoading={pending} loadingLabel={message("webapp.security.working")}>{message("webapp.reauthenticate.password.action")}</Button>
  </form>;
}

function proofFailureMessage(failure: ReauthenticationFailure): string {
  if (failure.kind === "invalid_password") return message("webapp.reauthenticate.password.invalid");
  if (failure.kind === "method_required") return message("webapp.reauthenticate.method_required");
  return securityFailureMessage(failure);
}
