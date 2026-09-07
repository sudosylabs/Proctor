import { useRef, useState, type FormEvent } from "react";

import { message } from "../../i18n/messages";
import { Button } from "../Button/Button";
import { InputField } from "../InputField/InputField";
import styles from "./AccountSecurity.module.css";

export function SecurityCodeForm({ onSubmit, pending, enrollment = false }: {
  onSubmit(code: string): Promise<void>;
  pending: boolean;
  enrollment?: boolean;
}) {
  const [code, setCode] = useState("");
  const [missing, setMissing] = useState(false);
  const input = useRef<HTMLInputElement>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    if (code.trim() === "") {
      setMissing(true);
      input.current?.focus();
      return;
    }
    const submitted = code.trim();
    setCode("");
    await onSubmit(submitted);
  }

  return (
    <form className={styles.stack} onSubmit={submit} aria-busy={pending} noValidate>
      <InputField
        ref={input}
        id="security-code"
        name="code"
        label={message(enrollment ? "webapp.security.code.authenticator" : "webapp.security.code.label")}
        description={message(enrollment ? "webapp.security.code.enrollment_help" : "webapp.security.code.help")}
        errorMessage={missing ? message("webapp.security.code.required") : undefined}
        autoComplete="one-time-code"
        autoCapitalize="none"
        spellCheck={false}
        required
        maxLength={256}
        inputMode={enrollment ? "numeric" : "text"}
        value={code}
        disabled={pending}
        onChange={(event) => { setCode(event.currentTarget.value); setMissing(false); }}
      />
      <Button type="submit" isLoading={pending} loadingLabel={message("webapp.security.working")}>
        {message(enrollment ? "webapp.security.setup.activate" : "webapp.security.challenge.verify")}
      </Button>
    </form>
  );
}
