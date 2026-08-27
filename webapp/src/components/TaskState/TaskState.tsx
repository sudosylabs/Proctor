import {
  type HTMLAttributes,
  type ReactNode,
  useEffect,
  useRef,
} from "react";

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
      <TaskHeading focus={focusHeading} id={headingID}>
        {heading}
      </TaskHeading>
      <p className={styles.body}>{body}</p>
      {children}
    </section>
  );
}

export interface TaskHeadingProps {
  children: ReactNode;
  focus?: boolean;
  id: string;
}

export function TaskHeading({
  children,
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
    <h1 ref={headingRef} id={id} tabIndex={focus ? -1 : undefined}>
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
