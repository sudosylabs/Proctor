import { forwardRef, useState } from "react";

import { Icon } from "../Icon/Icon";
import { InputField, type InputFieldProps } from "./InputField";
import styles from "./InputField.module.css";

export interface PasswordFieldProps
  extends Omit<InputFieldProps, "trailingControl" | "type"> {
  hidePasswordLabel: string;
  showPasswordLabel: string;
  toggleDisabled?: boolean;
}

export const PasswordField = forwardRef<HTMLInputElement, PasswordFieldProps>(
  function PasswordField(
    {
      hidePasswordLabel,
      showPasswordLabel,
      toggleDisabled = false,
      ...fieldProps
    },
    ref,
  ) {
    const [visible, setVisible] = useState(false);
    const toggleLabel = visible ? hidePasswordLabel : showPasswordLabel;

    return (
      <InputField
        {...fieldProps}
        ref={ref}
        type={visible ? "text" : "password"}
        trailingControl={
          <button
            className={styles.passwordToggle}
            type="button"
            aria-controls={fieldProps.id}
            aria-label={toggleLabel}
            aria-pressed={visible}
            disabled={toggleDisabled || fieldProps.disabled}
            title={toggleLabel}
            onClick={() => setVisible((current) => !current)}
          >
            <Icon
              name={visible ? "hidePassword" : "showPassword"}
              size="small"
            />
          </button>
        }
      />
    );
  },
);
