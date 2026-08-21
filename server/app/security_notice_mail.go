// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import appmail "github.com/sudosylabs/proctor/server/app/mail"

type accountStateMailPreparer interface {
	PrepareAccountStateChanged(appmail.NoticePreparation, bool) (*preparedDirectMail, error)
}

type sessionAdministrationMailPreparer interface {
	PrepareSessionsRevokedByAdministrator(appmail.NoticePreparation) (*preparedDirectMail, error)
}

type mfaNoticeMailPreparer interface {
	PrepareMFANotice(appmail.NoticePreparation, appmail.MFANoticeKind) (*preparedDirectMail, error)
}
