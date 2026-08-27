import { useEffect, useRef, useState } from "react";

import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { ProviderConnectionContent } from "../../components/ProviderConnection/ProviderConnectionContent";
import { message } from "../../i18n/messages";
import {
  beginProviderConnection,
  requestProviderConnectionContext,
  type ProviderConnectionContextResult,
} from "./ProviderConnectionApi";

export function ConnectProviderPage() {
  const [state, setState] = useState<ProviderConnectionContextResult>({
    kind: "unavailable",
  });
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  const requestRef = useRef<{
    attempt: number;
    promise: Promise<ProviderConnectionContextResult>;
  }>(undefined);

  useEffect(() => {
    let subscribed = true;
    if (requestRef.current?.attempt !== attempt) {
      requestRef.current = {
        attempt,
        promise: requestProviderConnectionContext(),
      };
    }
    void requestRef.current.promise.then((result) => {
      if (subscribed) {
        setState(result);
        setLoading(false);
      }
    });
    return () => {
      subscribed = false;
    };
  }, [attempt]);

  function retry() {
    setLoading(true);
    setAttempt((value) => value + 1);
  }

  return (
    <AccessPageShell
      mainSize="content"
      skipLabel={message("webapp.connect_provider.skip_to_main")}
      variant="single"
    >
      <ProviderConnectionContent
        loading={loading}
        state={state}
        beginConnection={beginProviderConnection}
        onRetry={retry}
        onRedirect={(url) => window.location.assign(url)}
      />
    </AccessPageShell>
  );
}
