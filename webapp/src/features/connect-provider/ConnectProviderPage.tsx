import { useAsyncResource } from "../../app/AsyncResource";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { ProviderConnectionContent } from "../../components/ProviderConnection/ProviderConnectionContent";
import { message } from "../../i18n/messages";
import {
  beginProviderConnection,
  requestProviderConnectionContext,
  type ProviderConnectionContextResult,
} from "./ProviderConnectionApi";

export function ConnectProviderPage() {
  const contextResource = useAsyncResource<ProviderConnectionContextResult>(
    requestProviderConnectionContext,
    { kind: "unavailable" },
  );

  return (
    <AccessPageShell
      mainSize="content"
      skipLabel={message("webapp.connect_provider.skip_to_main")}
      variant="single"
    >
      <ProviderConnectionContent
        loading={contextResource.loading}
        state={contextResource.value}
        beginConnection={beginProviderConnection}
        onRetry={contextResource.retry}
        onRedirect={(url) => window.location.assign(url)}
      />
    </AccessPageShell>
  );
}
