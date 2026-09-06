import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { DesktopAuthorizationContent } from "../../components/DesktopAuthorization/DesktopAuthorizationContent";
import { message } from "../../i18n/messages";
import type { DesktopAuthorizationProof } from "./DesktopAuthorizationApi";
import { useDesktopAuthorizationJourney } from "./DesktopAuthorizationJourney";

export interface DesktopAuthorizationPageProps {
  proof?: DesktopAuthorizationProof;
  state?: string;
}

export function DesktopAuthorizationPage({ proof, state }: DesktopAuthorizationPageProps) {
  const journey = useDesktopAuthorizationJourney({ proof, state, servingOrigin: window.location.origin });
  return (
    <AccessPageShell mainSize="content" skipLabel={message("webapp.desktop_authorization.skip_to_main")} variant="single">
      <DesktopAuthorizationContent journey={journey} />
    </AccessPageShell>
  );
}
