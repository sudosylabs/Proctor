// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/store"
)

type accountStateMailPreparer interface {
	PrepareAccountStateChanged(appmail.NoticePreparation, bool) (*store.PreparedMail, error)
}

type sessionAdministrationMailPreparer interface {
	PrepareSessionsRevokedByAdministrator(appmail.NoticePreparation) (*store.PreparedMail, error)
}

type mfaNoticeMailPreparer interface {
	PrepareMFANotice(appmail.NoticePreparation, appmail.MFANoticeKind) (*store.PreparedMail, error)
}
