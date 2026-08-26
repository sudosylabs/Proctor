import { useEffect } from "react";

import { AuthorizationCompletePage } from "../features/authorization-complete/AuthorizationCompletePage";
import { LoginPage } from "../features/login/LoginPage";
import { RegisterPage } from "../features/register/RegisterPage";
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
          : bootstrap.route === "/account/verify-email"
            ? message("webapp.verify_email.document_title")
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
    default:
      return null;
  }
}
