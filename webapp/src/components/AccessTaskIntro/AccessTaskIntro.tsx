import type { ReactNode } from "react";

import { TaskHeading } from "../TaskState/TaskState";
import styles from "./AccessTaskIntro.module.css";

export interface AccessTaskIntroProps {
  body: ReactNode;
  eyebrow: ReactNode;
  focusHeading?: boolean;
  heading: ReactNode;
  headingID: string;
}

export function AccessTaskIntro({
  body,
  eyebrow,
  focusHeading = false,
  heading,
  headingID,
}: AccessTaskIntroProps) {
  return (
    <header className={styles.intro}>
      <p className={styles.eyebrow}>{eyebrow}</p>
      <TaskHeading focus={focusHeading} id={headingID}>
        {heading}
      </TaskHeading>
      <p className={styles.body}>{body}</p>
    </header>
  );
}
