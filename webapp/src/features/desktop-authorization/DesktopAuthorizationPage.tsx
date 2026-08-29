import { useRef } from "react";

import { useAsyncResource } from "../../app/AsyncResource";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { DesktopAuthorizationContent } from "../../components/DesktopAuthorization/DesktopAuthorizationContent";
import { message } from "../../i18n/messages";
import {
  approveDesktopAuthorization,
  authenticateDesktopAuthorizationLocally,
  authenticateDesktopAuthorizationSession,
  bindDesktopAuthorization,
  cancelDesktopAuthorization,
  desktopAuthorizationProviderURL,
  requestDesktopAuthorizationContext,
  resetDesktopAuthorizationAccount,
  type DesktopAuthorizationProof,
  type DesktopContextResult,
} from "./DesktopAuthorizationApi";

export interface DesktopAuthorizationPageProps {
  proof?: DesktopAuthorizationProof;
  state?: string;
}

export function DesktopAuthorizationPage({
  proof,
  state,
}: DesktopAuthorizationPageProps) {
  const bindingEstablished = useRef(proof === undefined);
  const contextResource = useAsyncResource<DesktopContextResult>(
    async () => {
      if (state === undefined) {
        return { kind: "invalid" };
      }
      if (proof !== undefined && !bindingEstablished.current) {
        const binding = await bindDesktopAuthorization(proof);
        if (binding.kind !== "bound") {
          return binding;
        }
        bindingEstablished.current = true;
      }

      const loaded = await requestDesktopAuthorizationContext(
        window.location.origin,
      );
      if (loaded.kind !== "ready" || loaded.context.state !== "bound") {
        return loaded;
      }

      const session = await authenticateDesktopAuthorizationSession(
        loaded.context.installation,
      );
      if (session.kind === "authenticated") {
        return { kind: "ready", context: session.context };
      }
      if (session.kind === "no_session") {
        return loaded;
      }
      if (session.kind === "invalid" || session.kind === "locked") {
        return session;
      }
      return { kind: "unavailable" };
    },
    state === undefined ? { kind: "invalid" } : { kind: "unavailable" },
    state !== undefined,
  );

  return (
    <AccessPageShell
      mainSize="content"
      skipLabel={message("webapp.desktop_authorization.skip_to_main")}
      variant="single"
    >
      <DesktopAuthorizationContent
        checking={contextResource.loading}
        context={contextResource.value}
        state={state}
        approve={approveDesktopAuthorization}
        authenticateLocal={authenticateDesktopAuthorizationLocally}
        cancel={cancelDesktopAuthorization}
        providerURL={desktopAuthorizationProviderURL}
        resetAccount={resetDesktopAuthorizationAccount}
        reloadContext={requestDesktopAuthorizationContext}
        onContextChange={contextResource.replace}
        onRetryContext={contextResource.retry}
        onApproved={(redirectURL) => window.location.replace(redirectURL)}
        onProvider={(url) => window.location.assign(url)}
      />
    </AccessPageShell>
  );
}
