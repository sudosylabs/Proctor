import { useEffect, useRef, useState } from "react";

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

export function JoinPage({ claim }: JoinPageProps) {
  const [state, setState] = useState<InvitationStartResult>(
    claim === undefined ? { kind: "invalid" } : { kind: "unavailable" },
  );
  const [loading, setLoading] = useState(claim !== undefined);
  const [institutionName, setInstitutionName] = useState<string>();
  const requestRef = useRef<Promise<InvitationStartResult>>(undefined);
  const institutionRequestRef = useRef<Promise<string | undefined>>(undefined);

  useEffect(() => {
    if (claim === undefined) {
      return;
    }
    let subscribed = true;
    requestRef.current ??= startInvitation(claim);
    institutionRequestRef.current ??= requestInvitationInstitutionName(
      window.location.origin,
    );
    void Promise.all([requestRef.current, institutionRequestRef.current]).then(
      ([result, name]) => {
        if (subscribed) {
          setState(result);
          setInstitutionName(name);
          setLoading(false);
        }
      },
    );
    return () => {
      subscribed = false;
    };
  }, [claim]);

  return (
    <AccessPageShell
      mainSize="content"
      skipLabel={message("webapp.join.skip_to_main")}
      variant="single"
    >
      <InvitationContent
        institutionName={institutionName}
        loading={loading}
        state={state}
        acceptAccount={acceptInvitationAccount}
        acceptSession={acceptInvitationSession}
      />
    </AccessPageShell>
  );
}
