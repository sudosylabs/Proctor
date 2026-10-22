// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

// These aliases keep focused tests readable while production code refers to
// the owning mail package directly. They are not part of the application
// package's production interface.
import (
	"time"

	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
)

type FrozenMailContent = mail.FrozenContent
type DirectMailPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	Kind         model.MailOccurrenceKind
	TemplateKey  model.MailTemplateKey
	ActionURL    string
	At           time.Time
	Deadline     time.Time
	JobType      model.JobType
}
type AccountTokenMailPreparation = mail.AccountTokenPreparation
type NoticeMailPreparation = mail.NoticePreparation
type MFAMailNoticeKind = mail.MFANoticeKind
type MailAddress = mail.Address
type OutboundMail = mail.Outbound
type MailTransportOutcome = mail.TransportOutcome
type PersonalAccessTokenMailDetails = mail.PersonalAccessTokenDetails
type ExamManagerMailDetails = mail.ExamManagerDetails
type ClassTransitionMailDetails = mail.ClassTransitionDetails
type SubmissionReceiptMailDetails = mail.SubmissionReceiptDetails
type ResultReleaseMailDetails = mail.ResultReleaseDetails
type MailRenderRequest = mail.RenderRequest
type DirectMailTemplateRenderer = mail.Renderer
type MailDeliverySender = mail.Sender
type ClassTransitionMailPreparation = mail.ClassTransitionPreparation
type ResultReleaseDirectMailPreparation = mail.ResultReleasePreparation
type SubmissionReceiptMailPreparation = mail.SubmissionReceiptPreparation
type personalAccessTokenSecurityNoticePreparation = mail.PersonalAccessTokenPreparation
type relationshipTransitionMailPreparation = mail.RelationshipTransitionPreparation
type SittingScheduleMailDetails = mail.SittingScheduleDetails
type SittingMailTemplateRenderer = mail.SittingRenderer

const (
	MailTransportUnknown                  = mail.TransportUnknown
	MailTransportTemporary                = mail.TransportTemporary
	MailTransportPermanent                = mail.TransportPermanent
	MailTransportAcceptanceUncertain      = mail.TransportAcceptanceUncertain
	MFAMailNoticeEnabled                  = mail.MFANoticeEnabled
	MFAMailNoticeDisabled                 = mail.MFANoticeDisabled
	MFAMailNoticeRecoveryCodesRegenerated = mail.MFANoticeRecoveryCodesRegenerated
)
