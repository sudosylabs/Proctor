// Copyright (c) 2026 Sudosy Labs. SPDX-License-Identifier: AGPL-3.0-only
import { OTPInput, REGEXP_ONLY_DIGITS } from "input-otp";
import { forwardRef } from "react";

import "./InputOTPCompatibility.css";

import { RequiredMark, type InputFieldProps } from "./InputField";
import fieldStyles from "./InputField.module.css";
import styles from "./OneTimeCodeField.module.css";

interface OneTimeCodeFieldProps extends Pick<InputFieldProps,
  "id" | "name" | "label" | "description" | "describedBy" | "errorMessage" | "disabled" | "required"
> {
  value: string;
  onChange(value: string): void;
}

export const OneTimeCodeField = forwardRef<HTMLInputElement, OneTimeCodeFieldProps>(
  function OneTimeCodeField({ id, label, description, describedBy, errorMessage, required, disabled, ...props }, ref) {
    const descriptionID = description === undefined ? undefined : `${id}-description`;
    const errorID = errorMessage === undefined ? undefined : `${id}-error`;
    const references = [descriptionID, describedBy, errorID].filter(Boolean).join(" ");

    return <div className={fieldStyles.field}>
      <div className={fieldStyles.labelRow}>
        <label htmlFor={id}>{label}{required ? <RequiredMark /> : null}</label>
      </div>
      <OTPInput {...props} ref={ref} id={id} required={required} disabled={disabled}
        type="text" maxLength={6} pattern={REGEXP_ONLY_DIGITS} inputMode="numeric" autoComplete="one-time-code"
        autoCapitalize="none" spellCheck={false} dir="ltr"
        aria-describedby={references || undefined} aria-invalid={errorMessage === undefined ? undefined : true}
        containerClassName={styles.container} noScriptCSSFallback={null}
        pasteTransformer={(value) => value.replace(/[\s-]/g, "")}
        render={({ slots, isFocused, isHovering }) => <div className={styles.slots} aria-hidden="true"
          data-disabled={disabled || undefined} data-invalid={errorMessage !== undefined || undefined}
          data-hovering={isHovering || undefined} dir="ltr">
          {slots.map((slot, index) => <div key={index} className={styles.slot} data-proctor-otp-slot
            data-active={isFocused && slot.isActive || undefined}>
            {slot.char}
            {slot.hasFakeCaret ? <span className={styles.caret} /> : null}
          </div>)}
        </div>} />
      {description === undefined ? null : <p className={fieldStyles.description} id={descriptionID}>{description}</p>}
      {errorMessage === undefined ? null : <p className={fieldStyles.error} id={errorID}>{errorMessage}</p>}
    </div>;
  },
);
