// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import appmail "github.com/sudosylabs/proctor/server/app/mail"

type personalAccessTokenSecurityNoticeMailPreparer interface {
	PreparePersonalAccessTokenSecurityNotice(appmail.PersonalAccessTokenPreparation) (*preparedDirectMail, error)
}
