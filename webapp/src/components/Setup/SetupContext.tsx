import { message } from "../../i18n/messages";
import { Icon } from "../Icon/Icon";
import styles from "./Setup.module.css";

export function SetupContext() {
  const steps = [
    {
      label: message("webapp.setup.context.operator.label"),
      body: message("webapp.setup.context.operator.body"),
    },
    {
      label: message("webapp.setup.context.institution.label"),
      body: message("webapp.setup.context.institution.body"),
    },
    {
      label: message("webapp.setup.context.administrator.label"),
      body: message("webapp.setup.context.administrator.body"),
    },
  ];

  return (
    <div className={styles.context}>
      <p className={styles.eyebrow}>
        {message("webapp.setup.context.eyebrow")}
      </p>
      <p className={styles.contextHeading}>
        {message("webapp.setup.context.heading")}
      </p>
      <p className={styles.contextBody}>
        {message("webapp.setup.context.body")}
      </p>
      <ol className={styles.proofRail}>
        {steps.map((step, index) => (
          <li key={step.label}>
            <span className={styles.stepNumber} aria-hidden="true">
              {String(index + 1).padStart(2, "0")}
            </span>
            <span className={styles.stepCopy}>
              <strong>{step.label}</strong>
              <span>{step.body}</span>
            </span>
          </li>
        ))}
      </ol>
      <div className={styles.contextNote} role="note">
        <Icon className={styles.noteIcon} name="information" />
        <p>{message("webapp.setup.context.note")}</p>
      </div>
    </div>
  );
}
