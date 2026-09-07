import type { SecurityFailure } from "../../features/account-security/AccountSecurityApi";
import { message } from "../../i18n/messages";
import { ButtonLink } from "../Button/Button";
import { FormFeedback } from "../FormFeedback/FormFeedback";

export function securityFailureMessage(failure: SecurityFailure): string {
  switch (failure.kind) {
    case "no_session": return message("webapp.security.error.no_session");
    case "primary_required": return message("webapp.security.error.primary_required");
    case "strong_required": return message("webapp.security.error.strong_required");
    case "invalid_code": return message("webapp.security.error.invalid_code");
    case "rate_limited": return message("webapp.security.error.rate_limited");
    case "disabled": return message("webapp.security.error.disabled");
    case "conflict": return message("webapp.security.error.conflict");
    case "unavailable": return message("webapp.security.error.unavailable");
  }
}

export function SecurityFeedback({ failure, task = "security" }: {
  failure?: SecurityFailure;
  task?: "security" | "connect-provider";
}) {
  return (
    <>
      <FormFeedback message={failure === undefined ? undefined : securityFailureMessage(failure)} />
      {failure?.kind === "primary_required" || failure?.kind === "strong_required" ? (
        <ButtonLink href={`/account/reauthenticate?task=${task}`}>
          {message("webapp.reauthenticate.action")}
        </ButtonLink>
      ) : null}
      {failure?.kind === "no_session" ? (
        <ButtonLink href="/login">{message("webapp.security.sign_in")}</ButtonLink>
      ) : null}
    </>
  );
}
