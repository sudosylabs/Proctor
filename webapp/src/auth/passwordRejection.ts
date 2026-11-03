import type { MessageID } from "../i18n/messages";

/**
 * Why the server refused a new password. Each reason is a distinct public
 * error code; "length" also covers invalid text the browser cannot produce.
 */
export type PasswordRejection = "length" | "pattern" | "common" | "identity";

const rejectionByCode: Readonly<Record<string, PasswordRejection>> = {
  "authentication.password.invalid": "length",
  "authentication.password.pattern_invalid": "pattern",
  "authentication.password.common_invalid": "common",
  "authentication.password.identity_invalid": "identity",
};

/** Returns the rejection a Problem Details code names, if it is one. */
export function passwordRejection(
  code: string | undefined,
): PasswordRejection | undefined {
  return code !== undefined && Object.hasOwn(rejectionByCode, code)
    ? rejectionByCode[code]
    : undefined;
}

const sharedMessages: Readonly<
  Record<Exclude<PasswordRejection, "length">, MessageID>
> = {
  pattern: "webapp.password_policy.pattern",
  common: "webapp.password_policy.common",
  identity: "webapp.password_policy.identity",
};

/**
 * Selects the password field message for a rejection. Length rejections keep
 * the page's own wording, which states the installation's requirements.
 */
export function passwordRejectionMessage(
  rejection: PasswordRejection,
  lengthMessage: MessageID,
): MessageID {
  return rejection === "length" ? lengthMessage : sharedMessages[rejection];
}
