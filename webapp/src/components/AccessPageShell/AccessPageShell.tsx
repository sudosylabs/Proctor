import type { ReactNode } from "react";

import proctorLockupDark from "../../assets/brand/proctor-lockup-purple-white.svg";
import proctorLockupLight from "../../assets/brand/proctor-lockup.svg";
import { useProductTheme } from "../../theme/ProductTheme";
import styles from "./AccessPageShell.module.css";

export interface AccessPageShellProps {
  aside?: ReactNode;
  asideLabel?: string;
  children: ReactNode;
  mainSize?: "content" | "form";
  skipLabel: string;
  variant: "single" | "split" | "status";
}

export function AccessPageShell({
  aside,
  asideLabel,
  children,
  mainSize = "form",
  skipLabel,
  variant,
}: AccessPageShellProps) {
  const { effectiveTheme } = useProductTheme();
  const lockup =
    effectiveTheme.colorScheme === "dark"
      ? proctorLockupDark
      : proctorLockupLight;

  return (
    <>
      <a className="proctor-skip-link" href="#main-content">
        {skipLabel}
      </a>
      <div className={`${styles.shell} ${styles[variant]} ${styles[mainSize]}`}>
        <div className={styles.frame}>
          <header className={styles.header}>
            <img
              className={styles.brand}
              src={lockup}
              alt="Proctor"
              data-proctor-lockup-color-scheme={effectiveTheme.colorScheme}
              translate="no"
              width="163"
              height="32"
            />
          </header>

          <div className={styles.layout}>
            {aside === undefined ? null : (
              <aside className={styles.aside} aria-label={asideLabel}>
                {aside}
              </aside>
            )}
            <main id="main-content" className={styles.main} tabIndex={-1}>
              {children}
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
