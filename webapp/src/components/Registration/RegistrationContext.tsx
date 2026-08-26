import type { RegistrationDiscoveryState } from "../../features/register/RegistrationApi";
import { message } from "../../i18n/messages";
import styles from "./Registration.module.css";

function discoveryInstitutionName(
  state: RegistrationDiscoveryState,
): string | undefined {
  if (
    state.kind !== "ready" &&
    state.kind !== "setup" &&
    state.kind !== "invitation_required" &&
    state.kind !== "unavailable"
  ) {
    return undefined;
  }
  return state.discovery?.institution?.display_name;
}

export function RegistrationContext({
  state,
}: {
  state: RegistrationDiscoveryState;
}) {
  const proof = [
    message("webapp.register.proof.verification"),
    message("webapp.register.proof.access"),
    message("webapp.register.proof.credentials"),
  ];

  return (
    <div className={styles.context}>
      <p className={styles.eyebrow}>
        {discoveryInstitutionName(state) ??
          message("webapp.register.context.fallback")}
      </p>
      <p className={styles.contextHeading}>
        {message("webapp.register.context.heading")}
      </p>
      <p className={styles.contextBody}>
        {message("webapp.register.context.body")}
      </p>
      <ol className={styles.proofRail}>
        {proof.map((item) => (
          <li key={item}>
            <span className={styles.proofMark} aria-hidden="true" />
            <span>{item}</span>
          </li>
        ))}
      </ol>
      <p className={styles.contextNote}>
        {message("webapp.register.context.note")}
      </p>
    </div>
  );
}
