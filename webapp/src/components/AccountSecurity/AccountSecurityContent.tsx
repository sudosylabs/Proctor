import { useEffect, useRef, useState } from "react";

import type {
  AuthenticatorSetup, SecurityActions, SecurityContext, SecurityContextResult, SecurityFailure,
} from "../../features/account-security/AccountSecurityApi";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { Button, ButtonLink } from "../Button/Button";
import { InputField } from "../InputField/InputField";
import { Notice } from "../Notice/Notice";
import { TaskState, TaskStateActions, TaskStateAnnouncement } from "../TaskState/TaskState";
import { RecoveryCodes } from "./RecoveryCodes";
import { SecurityCodeForm } from "./SecurityCodeForm";
import { SecurityFeedback } from "./SecurityFeedback";
import styles from "./AccountSecurity.module.css";

type Task =
  | { kind: "status" }
  | { kind: "setup"; setup: AuthenticatorSetup }
  | { kind: "codes"; codes: string[] }
  | { kind: "challenge" }
  | { kind: "confirm_disable" }
  | { kind: "confirm_regenerate" }
  | { kind: "uncertain" };

export function AccountSecurityContent({ actions, loading, onRefresh, state }: {
  actions: SecurityActions;
  loading: boolean;
  onRefresh(): void;
  state: SecurityContextResult;
}) {
  const [task, setTask] = useState<Task>({ kind: "status" });
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<SecurityFailure>();
  const [focusHeading, setFocusHeading] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const viewGeneration = useRef(0);

  useEffect(() => {
    function discardSensitiveView() {
      viewGeneration.current += 1;
      setTask({ kind: "status" }); setFailure(undefined); setPending(false);
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

  function changeTask(next: Task) {
    setFailure(undefined);
    setFocusHeading(true);
    setTask(next);
    setAnnouncement("");
  }

  function refresh(successMessage = "") {
    changeTask({ kind: "status" });
    setAnnouncement(successMessage);
    onRefresh();
  }

  function failed(result: SecurityFailure, uncertainMutation = false) {
    if (uncertainMutation && result.kind === "unavailable") {
      changeTask({ kind: "uncertain" });
    } else {
      setFailure(result);
    }
  }

  async function setup() {
    if (pending) return;
    setPending(true); setFailure(undefined);
    const generation = viewGeneration.current;
    const result = await actions.setup();
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success") changeTask({ kind: "setup", setup: result.setup });
    else failed(result, true);
    setPending(false);
  }

  async function activate(code: string) {
    if (pending) return;
    setPending(true); setFailure(undefined);
    const generation = viewGeneration.current;
    const result = await actions.activate(code);
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success") changeTask({ kind: "codes", codes: result.codes });
    else failed(result, true);
    setPending(false);
  }

  async function challenge(code: string) {
    if (pending) return;
    setPending(true); setFailure(undefined);
    const generation = viewGeneration.current;
    const result = await actions.challenge(code);
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success") refresh(message("webapp.security.challenge.success"));
    else failed(result);
    setPending(false);
  }

  async function regenerate() {
    if (pending) return;
    setPending(true); setFailure(undefined);
    const generation = viewGeneration.current;
    const result = await actions.regenerate();
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success") changeTask({ kind: "codes", codes: result.codes });
    else failed(result, true);
    setPending(false);
  }

  async function disable() {
    if (pending) return;
    setPending(true); setFailure(undefined);
    const generation = viewGeneration.current;
    const result = await actions.disable();
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success") refresh(message("webapp.security.disable.success"));
    else failed(result, true);
    setPending(false);
  }

  async function signOut() {
    if (pending) return;
    setPending(true); setFailure(undefined);
    const generation = viewGeneration.current;
    const result = await actions.signOut();
    if (generation !== viewGeneration.current) return;
    if (result.kind === "success" || result.kind === "no_session") window.location.replace("/login");
    else failed(result);
    setPending(false);
  }

  if (loading || (state.kind !== "ready" && task.kind === "status")) {
    const noSession = !loading && state.kind === "no_session";
    const heading = message(loading ? "webapp.security.loading.heading" : noSession ? "webapp.security.no_session.heading" : "webapp.security.unavailable.heading");
    return <>
      <TaskStateAnnouncement message={heading} />
      <TaskState heading={heading} headingID="security-heading" focusHeading={focusHeading} busy={loading}
        body={message(loading ? "webapp.security.loading.body" : noSession ? "webapp.security.no_session.body" : "webapp.security.unavailable.body")}>
        {!loading ? <TaskStateActions>{noSession
          ? <ButtonLink href="/login">{message("webapp.security.sign_in")}</ButtonLink>
          : <Button onClick={() => refresh()}>{message("webapp.security.refresh")}</Button>}
        </TaskStateActions> : null}
      </TaskState>
    </>;
  }

  const context = state.kind === "ready" ? state.context : undefined;
  const content = taskCopy(task);
  return (
    <section className={styles.page} aria-labelledby="security-heading">
      <TaskStateAnnouncement message={pending ? message("webapp.security.working") : announcement} />
      <AccessTaskIntro key={task.kind} headingID="security-heading" focusHeading={focusHeading}
        eyebrow={message("webapp.security.eyebrow")} heading={content.heading} body={content.body} />
      {task.kind === "status" && context !== undefined ? <>
        <StatusSummary context={context} />
        {!context.serviceEnabled ? <Notice tone="warning">{message("webapp.security.service_disabled")}</Notice> : <>
          {context.enabled ? <div className={styles.stack}>
            <Button disabled={pending} onClick={() => changeTask({ kind: "challenge" })}>{message("webapp.security.challenge.action")}</Button>
            <Button disabled={pending} variant="secondary" onClick={() => changeTask({ kind: "confirm_regenerate" })}>{message("webapp.security.regenerate.action")}</Button>
            {!context.recoveryRequired ? <Button disabled={pending} variant="text" onClick={() => changeTask({ kind: "confirm_disable" })}>{message("webapp.security.disable.action")}</Button> : null}
          </div> : <Button isLoading={pending} loadingLabel={message("webapp.security.working")} onClick={setup}>
            {message(context.pending ? "webapp.security.setup.restart" : "webapp.security.setup.action")}
          </Button>}
          {context.pending ? <Notice>{message("webapp.security.setup.pending")}</Notice> : null}
        </>}
      </> : null}
      {task.kind === "setup" ? <>
        <Notice tone="warning">{message("webapp.security.setup.private")}</Notice>
        <InputField id="authenticator-secret" label={message("webapp.security.setup.key")} inputClassName={styles.secret}
          readOnly value={task.setup.secret} autoComplete="off" spellCheck={false} translate="no"
          description={message("webapp.security.setup.expires", { Time: new Date(task.setup.expiresAt).toLocaleString() })} />
        <SecurityCodeForm pending={pending} onSubmit={activate} enrollment />
      </> : null}
      {task.kind === "codes" ? <RecoveryCodes codes={task.codes} onDone={() => refresh(message("webapp.security.codes.saved"))} /> : null}
      {task.kind === "challenge" ? <SecurityCodeForm pending={pending} onSubmit={challenge} /> : null}
      {task.kind === "confirm_disable" ? <>
        <Notice tone="warning">{message("webapp.security.disable.warning")}</Notice>
        <Button isLoading={pending} loadingLabel={message("webapp.security.working")} onClick={disable}>{message("webapp.security.disable.confirm")}</Button>
      </> : null}
      {task.kind === "confirm_regenerate" ? <>
        <Notice tone="warning">{message("webapp.security.regenerate.warning")}</Notice>
        <Button isLoading={pending} loadingLabel={message("webapp.security.working")} onClick={regenerate}>{message("webapp.security.regenerate.confirm")}</Button>
      </> : null}
      {task.kind === "uncertain" ? <>
        <Notice tone="warning">{message("webapp.security.uncertain.guidance")}</Notice>
        <Button onClick={() => refresh()}>{message("webapp.security.refresh")}</Button>
      </> : null}
      <SecurityFeedback failure={failure} />
      {failure?.kind === "conflict" || failure?.kind === "disabled" ? <Button variant="secondary" onClick={() => refresh()}>{message("webapp.security.refresh")}</Button> : null}
      {task.kind !== "status" && task.kind !== "codes" && task.kind !== "uncertain" ? <Button variant="text" disabled={pending} onClick={() => refresh()}>{message("webapp.security.back")}</Button> : null}
      {task.kind === "status" ? <div className={styles.footer}>
        {!context?.recoveryRequired ? <a href="/authorization/complete">{message("webapp.security.return")}</a> : null}
        <Button variant="text" disabled={pending} onClick={signOut}>{message("webapp.security.sign_out")}</Button>
      </div> : null}
    </section>
  );
}

function StatusSummary({ context }: { context: SecurityContext }) {
  return <>
    {context.recoveryRequired ? <Notice tone="warning">{message("webapp.security.recovery_required")}</Notice> : null}
    <dl className={styles.summary}>
      <div><dt>{message("webapp.security.status.authenticator")}</dt><dd>{message(context.enabled ? "webapp.security.status.enabled" : "webapp.security.status.disabled")}</dd></div>
      {context.enabled ? <div><dt>{message("webapp.security.status.codes")}</dt><dd>{context.recoveryCodesRemaining}</dd></div> : null}
    </dl>
    {context.enabled && context.recoveryCodesRemaining === 0 ? <Notice tone="warning">{message("webapp.security.status.no_codes")}</Notice> : null}
  </>;
}

function taskCopy(task: Task) {
  switch (task.kind) {
    case "status": return { heading: message("webapp.security.heading"), body: message("webapp.security.body") };
    case "setup": return { heading: message("webapp.security.setup.heading"), body: message("webapp.security.setup.body") };
    case "codes": return { heading: message("webapp.security.codes.heading"), body: message("webapp.security.codes.body") };
    case "challenge": return { heading: message("webapp.security.challenge.heading"), body: message("webapp.security.challenge.body") };
    case "confirm_disable": return { heading: message("webapp.security.disable.heading"), body: message("webapp.security.disable.body") };
    case "confirm_regenerate": return { heading: message("webapp.security.regenerate.heading"), body: message("webapp.security.regenerate.body") };
    case "uncertain": return { heading: message("webapp.security.uncertain.heading"), body: message("webapp.security.uncertain.body") };
  }
}
