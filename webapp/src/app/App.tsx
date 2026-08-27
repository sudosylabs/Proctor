import { useEffect } from "react";

import { AuthorizationCompletePage } from "../features/authorization-complete/AuthorizationCompletePage";
import { ConnectProviderPage } from "../features/connect-provider/ConnectProviderPage";
import { DesktopAuthorizationPage } from "../features/desktop-authorization/DesktopAuthorizationPage";
import { ForgotPasswordPage } from "../features/forgot-password/ForgotPasswordPage";
import { JoinPage } from "../features/join/JoinPage";
import { LoginPage } from "../features/login/LoginPage";
import { RegisterPage } from "../features/register/RegisterPage";
import { ResetPasswordPage } from "../features/reset-password/ResetPasswordPage";
import { SetupPage } from "../features/setup/SetupPage";
import { VerifyEmailPage } from "../features/verify-email/VerifyEmailPage";
import { message } from "../i18n/messages";
import type { HostedPageBootstrap } from "./bootstrap";
import {
  defaultDocumentDescriptor,
  synchronizeDocument,
} from "./document";

export interface AppProps {
  bootstrap: HostedPageBootstrap;
}

export function App({ bootstrap }: AppProps) {
  const title =
    bootstrap.route === "/setup"
      ? message("webapp.setup.document_title")
      : bootstrap.route === "/login"
        ? message("webapp.login.document_title")
        : bootstrap.route === "/register"
          ? message("webapp.register.document_title")
          : bootstrap.route === "/authorize/desktop"
            ? message("webapp.desktop_authorization.document_title")
            : bootstrap.route === "/join"
              ? message("webapp.join.document_title")
              : bootstrap.route === "/account/forgot-password"
                ? message("webapp.forgot_password.document_title")
                : bootstrap.route === "/account/reset-password"
                  ? message("webapp.reset_password.document_title")
                  : bootstrap.route === "/account/verify-email"
                    ? message("webapp.verify_email.document_title")
                    : bootstrap.route === "/account/connect-provider"
                      ? message("webapp.connect_provider.document_title")
                      : bootstrap.route === "/authorization/complete"
                        ? message("webapp.authorization_complete.document_title")
                        : defaultDocumentDescriptor.title;

  useEffect(() => {
    synchronizeDocument(document, {
      ...defaultDocumentDescriptor,
      title,
    });
  }, [title]);

  switch (bootstrap.route) {
    case "/setup":
      return <SetupPage />;
    case "/login":
      return (
        <LoginPage
          externalLoginFailed={bootstrap.notice === "external_login_failed"}
        />
      );
    case "/register":
      return <RegisterPage />;
    case "/authorize/desktop": {
      const credential =
        bootstrap.credential?.kind === "desktop_browser_proof"
          ? bootstrap.credential
          : undefined;
      return (
        <DesktopAuthorizationPage
          proof={
            credential?.handle !== undefined && credential.state !== undefined
              ? {
                  browserProof: credential.value,
                  handle: credential.handle,
                  state: credential.state,
                }
              : undefined
          }
        />
      );
    }
    case "/join":
      return (
        <JoinPage
          claim={
            bootstrap.credential?.kind === "invitation_claim"
              ? bootstrap.credential.value
              : undefined
          }
        />
      );
    case "/account/forgot-password":
      return <ForgotPasswordPage />;
    case "/account/reset-password":
      return (
        <ResetPasswordPage
          token={
            bootstrap.credential?.kind === "password_reset_token"
              ? bootstrap.credential.value
              : undefined
          }
        />
      );
    case "/account/verify-email":
      return (
        <VerifyEmailPage
          token={
            bootstrap.credential?.kind === "email_verification_token"
              ? bootstrap.credential.value
              : undefined
          }
        />
      );
    case "/authorization/complete":
      return <AuthorizationCompletePage />;
    case "/account/connect-provider":
      return <ConnectProviderPage />;
    default:
      return null;
  }
}
