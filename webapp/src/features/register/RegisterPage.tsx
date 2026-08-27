import { useState } from "react";

import { useAsyncResource } from "../../app/AsyncResource";
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
  const discoveryResource = useAsyncResource<RegistrationDiscoveryState>(
    () => requestRegistrationDiscovery(window.location.origin),
    { kind: "loading" },
  );
  const state: RegistrationDiscoveryState = discoveryResource.loading
    ? { kind: "loading" }
    : discoveryResource.value;
  const [accepted, setAccepted] = useState(false);

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
        onPolicyChange={discoveryResource.replace}
        onRetry={discoveryResource.retry}
      />
    </AccessPageShell>
  );
}
