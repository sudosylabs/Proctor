import { EmailVerificationContent } from "../../components/EmailVerification/EmailVerificationContent";
import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { message } from "../../i18n/messages";
import { completeEmailVerification } from "./VerifyEmailApi";

export interface VerifyEmailPageProps {
  token?: string;
}

export function VerifyEmailPage({ token }: VerifyEmailPageProps) {
  return (
    <AccessPageShell
      skipLabel={message("webapp.verify_email.skip_to_main")}
      variant="status"
    >
      <EmailVerificationContent
        token={token}
        verifyEmail={completeEmailVerification}
      />
    </AccessPageShell>
  );
}
