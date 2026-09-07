import {
  type HTMLAttributes,
  type ReactNode,
  useEffect,
  useRef,
} from "react";

import { Loading } from "../Loading/Loading";
import styles from "./TaskState.module.css";

export interface TaskStateProps {
  body: ReactNode;
  busy?: boolean;
  children?: ReactNode;
  className?: string;
  focusHeading?: boolean;
  heading: ReactNode;
  headingID: string;
  label?: ReactNode;
}

export function TaskState({
  body,
  busy = false,
  children,
  className,
  focusHeading = false,
  heading,
  headingID,
  label,
}: TaskStateProps) {
  return (
    <section
      aria-busy={busy || undefined}
      aria-labelledby={headingID}
      className={classes(styles.state, className)}
    >
      {label === undefined ? null : <p className={styles.label}>{label}</p>}
      <TaskHeading className={busy ? styles.visuallyHidden : undefined} focus={focusHeading && !busy} id={headingID}>
        {heading}
      </TaskHeading>
      {busy ? (
        <div className={styles.loading}>
          <Loading label={body} showLabel size="large" announce={false} />
        </div>
      ) : <p className={styles.body}>{body}</p>}
      {children}
    </section>
  );
}

export interface TaskHeadingProps {
  children: ReactNode;
  className?: string;
  focus?: boolean;
  id: string;
}

export function TaskHeading({
  children,
  className,
  focus = false,
  id,
}: TaskHeadingProps) {
  const headingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (!focus) {
      return;
    }
    const frame = requestAnimationFrame(() => headingRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [focus]);

  return (
    <h1 className={className} ref={headingRef} id={id} tabIndex={focus ? -1 : undefined}>
      {children}
    </h1>
  );
}

export function TaskStateActions({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div {...props} className={classes(styles.actions, className)} />
  );
}

export function TaskStateAnnouncement({ message }: { message: string }) {
  return (
    <div
      className={styles.visuallyHidden}
      role="status"
      aria-atomic="true"
      aria-live="polite"
    >
      {message}
    </div>
  );
}

function classes(...values: Array<string | undefined>) {
  return values.filter((value): value is string => value !== undefined).join(" ");
}
