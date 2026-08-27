import { useAsyncResource } from "../../app/AsyncResource";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { InvitationContent } from "../../components/InvitationAcceptance/InvitationContent";
import { message } from "../../i18n/messages";
import {
  acceptInvitationAccount,
  acceptInvitationSession,
  requestInvitationInstitutionName,
  startInvitation,
  type InvitationStartResult,
} from "./InvitationApi";

export interface JoinPageProps {
  claim?: string;
}

interface JoinInitialResult {
  institutionName?: string;
  state: InvitationStartResult;
}

export function JoinPage({ claim }: JoinPageProps) {
  const initialResource = useAsyncResource<JoinInitialResult>(
    async () => {
      if (claim === undefined) {
        return { state: { kind: "invalid" } };
      }
      const [state, institutionName] = await Promise.all([
        startInvitation(claim),
        requestInvitationInstitutionName(window.location.origin),
      ]);
      return {
        state,
        ...(institutionName === undefined ? {} : { institutionName }),
      };
    },
    {
      state:
        claim === undefined ? { kind: "invalid" } : { kind: "unavailable" },
    },
    claim !== undefined,
  );

  return (
    <AccessPageShell
      mainSize="content"
      skipLabel={message("webapp.join.skip_to_main")}
      variant="single"
    >
      <InvitationContent
        institutionName={initialResource.value.institutionName}
        loading={initialResource.loading}
        state={initialResource.value.state}
        acceptAccount={acceptInvitationAccount}
        acceptSession={acceptInvitationSession}
      />
    </AccessPageShell>
  );
}
