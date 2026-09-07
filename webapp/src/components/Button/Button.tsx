import {
  forwardRef,
  type AnchorHTMLAttributes,
  type ButtonHTMLAttributes,
  type ReactNode,
} from "react";

import { Loading } from "../Loading/Loading";
import styles from "./Button.module.css";

export type ButtonVariant = "primary" | "secondary" | "text";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  isLoading?: boolean;
  loadingLabel?: ReactNode;
  variant?: ButtonVariant;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  function Button(
    {
      children,
      className,
      disabled = false,
      isLoading = false,
      loadingLabel,
      type = "button",
      variant = "primary",
      ...props
    },
    ref,
  ) {
    return (
      <button
        {...props}
        ref={ref}
        aria-busy={isLoading || undefined}
        className={classes(styles.button, styles[variant], className)}
        disabled={disabled || isLoading}
        type={type}
      >
        <span className={styles.content} aria-hidden={isLoading || undefined}>
          {children}
        </span>
        {isLoading ? (
          <Loading className={styles.loading} label={loadingLabel ?? children} announce={false} />
        ) : null}
      </button>
    );
  },
);

export interface ButtonLinkProps
  extends AnchorHTMLAttributes<HTMLAnchorElement> {
  variant?: Exclude<ButtonVariant, "text">;
}

export const ButtonLink = forwardRef<HTMLAnchorElement, ButtonLinkProps>(
  function ButtonLink(
    { className, variant = "primary", ...props },
    ref,
  ) {
    return (
      <a
        {...props}
        ref={ref}
        className={classes(styles.button, styles[variant], className)}
      />
    );
  },
);

function classes(...values: Array<string | undefined>) {
  return values.filter((value): value is string => value !== undefined).join(" ");
}
