// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
)

const securityNoticeDeliveryLifetime = 24 * time.Hour

type securityNoticePreparation = appmail.SecurityNoticePreparation

type securityNoticeMailPreparer interface {
	PrepareSecurityNotice(securityNoticePreparation) (*preparedDirectMail, error)
}
