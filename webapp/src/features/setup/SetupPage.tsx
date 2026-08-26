import { useEffect, useRef, useState } from "react";

import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { SetupContent } from "../../components/Setup/SetupContent";
import { SetupContext } from "../../components/Setup/SetupContext";
import { message } from "../../i18n/messages";
import {
  requestInstallationStatus,
  type SetupStatus,
  submitInstallation,
} from "./SetupApi";

export function SetupPage() {
  const [status, setStatus] = useState<SetupStatus>({ kind: "loading" });
  const [statusAttempt, setStatusAttempt] = useState(0);
  const statusRequest = useRef<{
    attempt: number;
    promise: Promise<SetupStatus>;
  }>(undefined);

  useEffect(() => {
    let subscribed = true;
    if (statusRequest.current?.attempt !== statusAttempt) {
      statusRequest.current = {
        attempt: statusAttempt,
        promise: requestInstallationStatus(),
      };
    }
    void statusRequest.current.promise.then((result) => {
      if (subscribed) {
        setStatus(result);
      }
    });
    return () => {
      subscribed = false;
    };
  }, [statusAttempt]);

  function retryStatus() {
    setStatus({ kind: "loading" });
    setStatusAttempt((attempt) => attempt + 1);
  }

  return (
    <AccessPageShell
      aside={<SetupContext />}
      asideLabel={message("webapp.setup.context.eyebrow")}
      mainSize="content"
      skipLabel={message("webapp.setup.skip_to_main")}
      variant="split"
    >
      <SetupContent
        status={status}
        submitSetup={submitInstallation}
        onComplete={() => setStatus({ kind: "complete" })}
        onRetry={retryStatus}
      />
    </AccessPageShell>
  );
}
