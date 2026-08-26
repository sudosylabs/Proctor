import type { ReactNode } from "react";

import proctorLockupDark from "../../assets/brand/proctor-lockup-purple-white.svg";
import proctorLockupLight from "../../assets/brand/proctor-lockup.svg";
import styles from "./AccessPageShell.module.css";

export interface AccessPageShellProps {
  aside?: ReactNode;
  asideLabel?: string;
  children: ReactNode;
  mainSize?: "content" | "form";
  skipLabel: string;
  variant: "split" | "status";
}

export function AccessPageShell({
  aside,
  asideLabel,
  children,
  mainSize = "form",
  skipLabel,
  variant,
}: AccessPageShellProps) {
  return (
    <>
      <a className="proctor-skip-link" href="#main-content">
        {skipLabel}
      </a>
      <div className={`${styles.shell} ${styles[variant]} ${styles[mainSize]}`}>
        <div className={styles.frame}>
          <header className={styles.header}>
            <picture className={styles.brand}>
              <source
                media="(prefers-color-scheme: dark)"
                srcSet={proctorLockupDark}
              />
              <img
                src={proctorLockupLight}
                alt="Proctor"
                translate="no"
                width="163"
                height="32"
              />
            </picture>
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
