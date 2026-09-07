// Copyright (c) 2026 Sudosy Labs. SPDX-License-Identifier: AGPL-3.0-only
import { QRCodeSVG } from "qrcode.react";

import type { AuthenticatorSetup as Setup } from "../../features/account-security/AccountSecurityApi";
import { message } from "../../i18n/messages";
import { InputField } from "../InputField/InputField";
import styles from "./AuthenticatorSetup.module.css";

export function AuthenticatorSetup({ setup }: { setup: Setup }) {
  return <div className={styles.setup}>
    <QRCodeSVG className={styles.qr} value={setup.provisioningURI}
      size={240} level="M" marginSize={4}
      bgColor="var(--proctor-color-machine-readable-background)"
      fgColor="var(--proctor-color-machine-readable-foreground)"
      role="img" aria-label={message("webapp.security.setup.qr_label")}
      aria-describedby="authenticator-setup-expiry" />
    <details className={styles.manual}>
      <summary>{message("webapp.security.setup.manual")}</summary>
      <div className={styles.manualContent}>
        <InputField id="authenticator-secret" label={message("webapp.security.setup.key")}
          inputClassName={styles.secret} readOnly value={setup.secret} autoComplete="off"
          spellCheck={false} translate="no" description={message("webapp.security.setup.manual_help")} />
      </div>
    </details>
    <p className={styles.expiry} id="authenticator-setup-expiry">
      {message("webapp.security.setup.expires", { Time: new Date(setup.expiresAt).toLocaleString() })}
    </p>
  </div>;
}
