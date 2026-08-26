import { useEffect, useRef, useState } from "react";

import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { RegistrationContent } from "../../components/Registration/RegistrationContent";
import { RegistrationContext } from "../../components/Registration/RegistrationContext";
import { message } from "../../i18n/messages";
import {
  requestRegistrationDiscovery,
  type RegistrationDiscoveryState,
  submitRegistration,
} from "./RegistrationApi";

export function RegisterPage() {
  const [state, setState] = useState<RegistrationDiscoveryState>({
    kind: "loading",
  });
  const [accepted, setAccepted] = useState(false);
  const [discoveryAttempt, setDiscoveryAttempt] = useState(0);
  const discoveryRequest = useRef<{
    attempt: number;
    promise: Promise<RegistrationDiscoveryState>;
  }>(undefined);

  useEffect(() => {
    let subscribed = true;
    if (discoveryRequest.current?.attempt !== discoveryAttempt) {
      discoveryRequest.current = {
        attempt: discoveryAttempt,
        promise: requestRegistrationDiscovery(window.location.origin),
      };
    }
    void discoveryRequest.current.promise.then((result) => {
      if (subscribed) {
        setState(result);
      }
    });
    return () => {
      subscribed = false;
    };
  }, [discoveryAttempt]);

  function retryDiscovery() {
    setState({ kind: "loading" });
    setDiscoveryAttempt((attempt) => attempt + 1);
  }

  return (
    <AccessPageShell
      aside={<RegistrationContext state={state} />}
      asideLabel={message("webapp.register.context.heading")}
      skipLabel={message("webapp.register.skip_to_main")}
      variant="split"
    >
      <RegistrationContent
        accepted={accepted}
        state={state}
        submitRegistration={submitRegistration}
        onAccepted={() => setAccepted(true)}
        onPolicyChange={setState}
        onRetry={retryDiscovery}
      />
    </AccessPageShell>
  );
}
