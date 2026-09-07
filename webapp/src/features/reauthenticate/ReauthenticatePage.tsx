import { useAsyncResource } from "../../app/AsyncResource";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { ReauthenticationContent } from "../../components/Reauthentication/ReauthenticationContent";
import { message } from "../../i18n/messages";
import { challengeSession, requestSecurityContext, type SecurityContextResult } from "../account-security/AccountSecurityApi";
import { beginExternalProof, proveCurrentPassword, type ReauthenticationActions, type ReauthenticationTask } from "./ReauthenticationApi";

const actions: ReauthenticationActions = { password: proveCurrentPassword, external: beginExternalProof, challenge: challengeSession };

export function ReauthenticatePage({ task = "security", externalProofFailed = false }: {
  task?: ReauthenticationTask;
  externalProofFailed?: boolean;
}) {
  const resource = useAsyncResource<SecurityContextResult>(requestSecurityContext, { kind: "unavailable" });
  return (
    <AccessPageShell skipLabel={message("webapp.security.skip_to_main")} variant="single" mainSize="form">
      <ReauthenticationContent actions={actions} task={task} loading={resource.loading} state={resource.value}
        onRefresh={resource.retry} externalProofFailed={externalProofFailed} />
    </AccessPageShell>
  );
}
