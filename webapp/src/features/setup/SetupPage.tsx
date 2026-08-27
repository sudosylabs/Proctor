import { useAsyncResource } from "../../app/AsyncResource";
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
  const statusResource = useAsyncResource<SetupStatus>(
    requestInstallationStatus,
    { kind: "loading" },
  );
  const status: SetupStatus = statusResource.loading
    ? { kind: "loading" }
    : statusResource.value;

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
        onComplete={() => statusResource.replace({ kind: "complete" })}
        onRetry={statusResource.retry}
      />
    </AccessPageShell>
  );
}
