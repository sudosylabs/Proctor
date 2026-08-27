import { useEffect, useRef, useState } from "react";

import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { DesktopAuthorizationContent } from "../../components/DesktopAuthorization/DesktopAuthorizationContent";
import { message } from "../../i18n/messages";
import {
  approveDesktopAuthorization,
  cancelDesktopAuthorization,
  requestDesktopAuthorizationContext,
  type DesktopAuthorizationProof,
  type DesktopContextResult,
} from "./DesktopAuthorizationApi";

export interface DesktopAuthorizationPageProps {
  proof?: DesktopAuthorizationProof;
}

export function DesktopAuthorizationPage({
  proof,
}: DesktopAuthorizationPageProps) {
  const [context, setContext] = useState<DesktopContextResult>({
    kind: "unavailable",
  });
  const [checking, setChecking] = useState(proof !== undefined);
  const [attempt, setAttempt] = useState(0);
  const requestRef = useRef<{
    attempt: number;
    promise: Promise<DesktopContextResult>;
  }>(undefined);

  useEffect(() => {
    if (proof === undefined) {
      return;
    }
    let subscribed = true;
    if (requestRef.current?.attempt !== attempt) {
      requestRef.current = {
        attempt,
        promise: requestDesktopAuthorizationContext(window.location.origin),
      };
    }
    void requestRef.current.promise.then((result) => {
      if (subscribed) {
        setContext(result);
        setChecking(false);
      }
    });
    return () => {
      subscribed = false;
    };
  }, [attempt, proof]);

  function retryContext() {
    setChecking(true);
    setAttempt((value) => value + 1);
  }

  return (
    <AccessPageShell
      mainSize="content"
      skipLabel={message("webapp.desktop_authorization.skip_to_main")}
      variant="single"
    >
      <DesktopAuthorizationContent
        checking={checking}
        context={context}
        proof={proof}
        approve={approveDesktopAuthorization}
        cancel={cancelDesktopAuthorization}
        onRetryContext={retryContext}
        onApproved={(redirectURL) => window.location.replace(redirectURL)}
      />
    </AccessPageShell>
  );
}
