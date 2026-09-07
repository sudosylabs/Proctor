import { useEffect, useState } from "react";

import { message } from "../../i18n/messages";
import { Button } from "../Button/Button";
import { Notice } from "../Notice/Notice";
import styles from "./AccountSecurity.module.css";

export function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone(): void }) {
  const [acknowledged, setAcknowledged] = useState(false);

  useEffect(() => {
    function warnBeforeLeaving(event: BeforeUnloadEvent) { event.preventDefault(); event.returnValue = ""; }
    window.addEventListener("beforeunload", warnBeforeLeaving);
    return () => window.removeEventListener("beforeunload", warnBeforeLeaving);
  }, []);

  function download() {
    const url = URL.createObjectURL(new Blob([`${codes.join("\n")}\n`], { type: "text/plain;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "proctor-recovery-codes.txt";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  return (
    <div className={styles.stack}>
      <Notice tone="warning">{message("webapp.security.codes.warning")}</Notice>
      <ul className={styles.codes} aria-label={message("webapp.security.codes.list")} translate="no">
        {codes.map((code) => <li key={code}><code>{code}</code></li>)}
      </ul>
      <Button variant="secondary" onClick={download}>{message("webapp.security.codes.download")}</Button>
      <label className={styles.acknowledgement}>
        <input type="checkbox" checked={acknowledged} onChange={(event) => setAcknowledged(event.currentTarget.checked)} />
        <span>{message("webapp.security.codes.acknowledge")}</span>
      </label>
      <Button disabled={!acknowledged} onClick={onDone}>{message("webapp.security.codes.done")}</Button>
    </div>
  );
}
