import type { ReactNode } from "react";

import { Notice } from "../Notice/Notice";
import styles from "./FormFeedback.module.css";

export interface FormFeedbackProps {
  className?: string;
  message?: ReactNode;
}

export function FormFeedback({ className, message }: FormFeedbackProps) {
  return (
    <div
      aria-atomic="true"
      aria-live="polite"
      className={classes(styles.feedback, className)}
      data-proctor-form-feedback=""
      role="status"
    >
      {message === undefined ? null : <Notice tone="danger">{message}</Notice>}
    </div>
  );
}

function classes(...values: Array<string | undefined>) {
  return values.filter((value): value is string => value !== undefined).join(" ");
}
