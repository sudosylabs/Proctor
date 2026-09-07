import type { ReactNode } from "react";

import { Icon, type IconSize } from "../Icon/Icon";
import styles from "./Loading.module.css";

export interface LoadingProps {
  label: ReactNode;
  showLabel?: boolean;
  size?: IconSize;
  announce?: boolean;
  className?: string;
}

export function Loading({
  label,
  showLabel = false,
  size = "default",
  announce = true,
  className,
}: LoadingProps) {
  return (
    <span
      className={[styles.loading, className].filter(Boolean).join(" ")}
      data-proctor-loading
      role={announce ? "status" : undefined}
      aria-live={announce ? "polite" : undefined}
      aria-atomic={announce ? true : undefined}
    >
      <Icon className={styles.spinner} name="loading" size={size} />
      <span className={showLabel ? styles.label : styles.visuallyHidden}>{label}</span>
    </span>
  );
}
