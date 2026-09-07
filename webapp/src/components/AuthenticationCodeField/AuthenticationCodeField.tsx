// Copyright (c) 2026 Sudosy Labs. SPDX-License-Identifier: AGPL-3.0-only
import { forwardRef, useCallback, useRef } from "react";

import { message } from "../../i18n/messages";
import { Button } from "../Button/Button";
import { InputField } from "../InputField/InputField";
import { OneTimeCodeField } from "../InputField/OneTimeCodeField";
import styles from "./AuthenticationCodeField.module.css";

export interface AuthenticationCodeValue {
  kind: "authenticator" | "recovery";
  code: string;
}

export function isCompleteAuthenticationCode(value: AuthenticationCodeValue): boolean {
  return value.kind === "authenticator" ? /^[0-9]{6}$/.test(value.code) : value.code.trim().length > 0;
}

interface AuthenticationCodeFieldProps {
  id: string;
  name: string;
  value: AuthenticationCodeValue;
  onChange(value: AuthenticationCodeValue): void;
  allowRecovery?: boolean;
  disabled?: boolean;
  errorMessage?: string;
  describedBy?: string;
}

export const AuthenticationCodeField = forwardRef<HTMLInputElement, AuthenticationCodeFieldProps>(
  function AuthenticationCodeField({ value, onChange, allowRecovery = true, ...props }, ref) {
    const input = useRef<HTMLInputElement>(null);
    const attachInput = useCallback((node: HTMLInputElement | null) => {
      input.current = node;
      if (typeof ref === "function") ref(node);
      else if (ref !== null) ref.current = node;
    }, [ref]);
    const recovery = allowRecovery && value.kind === "recovery";
    const shared = { ...props, ref: attachInput, required: true, value: value.code };

    function switchMethod() {
      onChange({ kind: recovery ? "authenticator" : "recovery", code: "" });
      requestAnimationFrame(() => input.current?.focus());
    }

    return <div className={styles.field}>
      {recovery ? <InputField {...shared} label={message("webapp.form.recovery_code.label")}
        description={message("webapp.form.recovery_code.help")}
        autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={256}
        onChange={(event) => onChange({ kind: "recovery", code: event.currentTarget.value })} />
        : <OneTimeCodeField {...shared} label={message("webapp.security.code.authenticator")}
          description={message("webapp.form.otp.help")}
          onChange={(code) => onChange({ kind: "authenticator", code })} />}
      {allowRecovery ? <Button className={styles.switchMethod} variant="text" disabled={props.disabled}
        aria-controls={props.id} onClick={switchMethod}>
        {message(recovery ? "webapp.form.otp.use" : "webapp.form.recovery_code.use")}
      </Button> : null}
    </div>;
  },
);
