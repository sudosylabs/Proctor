import { useAsyncResource } from "../../app/AsyncResource";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { AccountSecurityContent } from "../../components/AccountSecurity/AccountSecurityContent";
import { message } from "../../i18n/messages";
import {
  activateAuthenticator, beginAuthenticatorSetup, challengeSession, disableAuthenticator,
  regenerateRecoveryCodes, requestSecurityContext, signOutSecuritySession, type SecurityActions, type SecurityContextResult,
} from "./AccountSecurityApi";

const actions: SecurityActions = {
  setup: beginAuthenticatorSetup, activate: activateAuthenticator, challenge: challengeSession,
  regenerate: regenerateRecoveryCodes, disable: disableAuthenticator, signOut: signOutSecuritySession,
};

export function AccountSecurityPage() {
  const resource = useAsyncResource<SecurityContextResult>(requestSecurityContext, { kind: "unavailable" });
  return (
    <AccessPageShell skipLabel={message("webapp.security.skip_to_main")} variant="single" mainSize="form">
      <AccountSecurityContent actions={actions} loading={resource.loading} state={resource.value} onRefresh={resource.retry} />
    </AccessPageShell>
  );
}
