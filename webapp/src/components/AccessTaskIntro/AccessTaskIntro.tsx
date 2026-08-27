import type { ReactNode } from "react";

import styles from "./AccessTaskIntro.module.css";

export interface AccessTaskIntroProps {
  body: ReactNode;
  eyebrow: ReactNode;
  heading: ReactNode;
  headingID: string;
}

export function AccessTaskIntro({
  body,
  eyebrow,
  heading,
  headingID,
}: AccessTaskIntroProps) {
  return (
    <header className={styles.intro}>
      <p className={styles.eyebrow}>{eyebrow}</p>
      <h1 id={headingID}>{heading}</h1>
      <p className={styles.body}>{body}</p>
    </header>
  );
}
