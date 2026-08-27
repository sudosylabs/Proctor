import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { ForgotPasswordContent } from "../../components/PasswordRecovery/ForgotPasswordContent";
import { message } from "../../i18n/messages";
import { requestPasswordReset } from "./ForgotPasswordApi";

export function ForgotPasswordPage() {
  return (
    <AccessPageShell
      mainSize="content"
      skipLabel={message("webapp.forgot_password.skip_to_main")}
      variant="single"
    >
      <ForgotPasswordContent requestReset={requestPasswordReset} />
    </AccessPageShell>
  );
}
