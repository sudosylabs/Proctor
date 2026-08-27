import {
  forwardRef,
  type HTMLAttributes,
} from "react";

import styles from "./Notice.module.css";

export type NoticeTone =
  | "accent"
  | "information"
  | "success"
  | "warning"
  | "danger";

export interface NoticeProps extends HTMLAttributes<HTMLDivElement> {
  tone?: NoticeTone;
}

export const Notice = forwardRef<HTMLDivElement, NoticeProps>(function Notice(
  { className, tone = "information", ...props },
  ref,
) {
  return (
    <div
      {...props}
      ref={ref}
      className={classes(styles.notice, styles[tone], className)}
      data-proctor-notice=""
      data-proctor-notice-tone={tone}
    />
  );
});

function classes(...values: Array<string | undefined>) {
  return values.filter((value): value is string => value !== undefined).join(" ");
}
