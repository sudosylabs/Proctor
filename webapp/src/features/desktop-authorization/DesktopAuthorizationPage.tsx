import { useAsyncResource } from "../../app/AsyncResource";
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
  const contextResource = useAsyncResource<DesktopContextResult>(
    () => requestDesktopAuthorizationContext(window.location.origin),
    { kind: "unavailable" },
    proof !== undefined,
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
        proof={proof}
        approve={approveDesktopAuthorization}
        cancel={cancelDesktopAuthorization}
        onRetryContext={contextResource.retry}
        onApproved={(redirectURL) => window.location.replace(redirectURL)}
      />
    </AccessPageShell>
  );
}
