// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// This file contains direct-mail composition and shared frozen-payload rules.
package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	DeliverySealingPurpose = "mail.delivery"
)

type FrozenContent struct{ Subject, Text, HTML string }

type Address struct {
	Name    string
	Address string
}

type Outbound struct {
	From         Address
	EnvelopeFrom string
	To           Address
	Subject      string
	Text         string
	HTML         string
	Headers      map[string][]string
	MessageID    string
	Date         time.Time
}

type TransportOutcome string

const (
	TransportUnknown             TransportOutcome = "unknown"
	TransportTemporary           TransportOutcome = "temporary"
	TransportPermanent           TransportOutcome = "permanent"
	TransportAcceptanceUncertain TransportOutcome = "acceptance_uncertain"
)

type Sender interface {
	Enabled() bool
	From() Address
	Send(context.Context, Outbound) (TransportOutcome, error)
}

type PersonalAccessTokenDetails struct {
	Description        string
	ExpiresAt          time.Time
	ActionAt           time.Time
	ActionCount        int
	AcademicUnitScoped bool
}

type ExamManagerDetails struct {
	Title        string
	Relationship string
	ActionAt     time.Time
}

type ClassTransitionDetails struct {
	PreviousClassDisplayName string
	ClassDisplayName         string
	StartsAt                 time.Time
	EndsAt                   time.Time
}

type SubmissionReceiptDetails struct {
	ExamTitle    string
	SittingID    model.ExamSittingID
	SubmissionID model.SubmissionID
	SealedAt     time.Time
}

type ResultReleaseDetails struct {
	ExamTitle  string
	ReleasedAt time.Time
}

// Presentation is the closed set of safe, typed facts accepted by mail
// rendering. Only this package can add a presentation family.
type Presentation interface{ mailPresentation() }

func (PersonalAccessTokenDetails) mailPresentation() {}
func (ExamManagerDetails) mailPresentation()         {}
func (ClassTransitionDetails) mailPresentation()     {}
func (SubmissionReceiptDetails) mailPresentation()   {}
func (ResultReleaseDetails) mailPresentation()       {}

type RenderRequest struct {
	Key          model.MailTemplateKey
	Locale       string
	ActionURL    string
	Presentation Presentation
}

type Renderer interface {
	Render(RenderRequest) (FrozenContent, error)
}

type FrozenPayloadV1 struct {
	Version              int    `json:"version"`
	RecipientName        string `json:"recipient_name"`
	RecipientAddress     string `json:"recipient_address"`
	FromName             string `json:"from_name"`
	FromAddress          string `json:"from_address"`
	Subject              string `json:"subject"`
	Text                 string `json:"text"`
	HTML                 string `json:"html"`
	AutoSubmitted        string `json:"auto_submitted"`
	AutoResponseSuppress string `json:"auto_response_suppress"`
}

type directPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	ActionURL    string
	At           time.Time
	Deadline     time.Time
}

// AccountTokenPreparation contains only the token facts a caller owns. Mail
// chooses the occurrence meaning, delivery class, and template.
type AccountTokenPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	ActionURL    string
	At           time.Time
	Deadline     time.Time
}

// NoticePreparation contains the recipient and committed action time shared by
// ordinary one-recipient notices.
type NoticePreparation struct {
	Recipient *model.User
	At        time.Time
}

type MFANoticeKind string

const (
	MFANoticeEnabled                  MFANoticeKind = "enabled"
	MFANoticeDisabled                 MFANoticeKind = "disabled"
	MFANoticeRecoveryCodesRegenerated MFANoticeKind = "recovery_codes_regenerated"
)

type PersonalAccessTokenPreparation struct {
	Recipient          *model.User
	TemplateKey        model.MailTemplateKey
	Description        string
	ExpiresAt          time.Time
	ActionAt           time.Time
	ActionCount        int
	AcademicUnitScoped bool
}

type ClassTransitionPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	Details      ClassTransitionDetails
	ActionAt     time.Time
}

type SubmissionReceiptPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	Details      SubmissionReceiptDetails
	ActionAt     time.Time
}

type ResultReleasePreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	Details      ResultReleaseDetails
	ReleasedAt   time.Time
}

// RelationshipTransitionPreparation covers the generic Academic Unit
// membership and Role Binding notices. Their templates intentionally contain
// no privileged scope or role detail.
type RelationshipTransitionPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	ActionAt     time.Time
}

type Composer struct {
	renderer Renderer
	sender   Sender
	sealer   *secretseal.Sealer
}

func NewComposer(renderer Renderer, sender Sender, sealer *secretseal.Sealer) (*Composer, error) {
	if renderer == nil || sender == nil || sender.Enabled() && sealer == nil {
		return nil, errors.New("mail composer dependencies are invalid")
	}
	return &Composer{renderer: renderer, sender: sender, sealer: sealer}, nil
}

func (p *Composer) Enabled() bool {
	return p != nil && p.sender != nil && p.sender.Enabled() && p.sealer != nil
}

func (p *Composer) prepareDirect(request directPreparation) (*store.PreparedMail, error) {
	user := request.Recipient
	definition, defined := definitionFor(request.TemplateKey)
	if p == nil || p.sender == nil || user == nil || user.Validate() != nil || !user.IsActive() ||
		!request.OccurrenceID.IsValid() || !defined ||
		request.At.IsZero() || !request.Deadline.After(request.At) || definition.actionRequired != (strings.TrimSpace(request.ActionURL) != "") {
		return nil, errors.New("direct mail input is invalid")
	}
	return p.prepareRecipient(user.DisplayName, user.Email, user.Locale, user.ID, user.ID, "", request.OccurrenceID,
		request.TemplateKey, request.ActionURL, request.At, request.Deadline, nil)
}

func (p *Composer) PrepareEmailVerification(request AccountTokenPreparation) (*store.PreparedMail, error) {
	return p.prepareAccountToken(request, model.MailTemplateIdentityVerifyEmail)
}

func (p *Composer) PreparePasswordReset(request AccountTokenPreparation) (*store.PreparedMail, error) {
	return p.prepareAccountToken(request, model.MailTemplateIdentityPasswordReset)
}

func (p *Composer) PrepareEmailChangeVerification(request AccountTokenPreparation) (*store.PreparedMail, error) {
	return p.prepareAccountToken(request, model.MailTemplateIdentityEmailChangeVerifyNew)
}

func (p *Composer) prepareAccountToken(request AccountTokenPreparation, key model.MailTemplateKey) (*store.PreparedMail, error) {
	return p.prepareDirect(directPreparation{Recipient: request.Recipient, OccurrenceID: request.OccurrenceID,
		TemplateKey: key, ActionURL: request.ActionURL, At: request.At, Deadline: request.Deadline})
}

func (p *Composer) PreparePasswordChanged(request NoticePreparation) (*store.PreparedMail, error) {
	return p.prepareNotice(request, model.MailTemplateIdentityPasswordChanged)
}

func (p *Composer) PrepareEmailChangeWarning(request NoticePreparation) (*store.PreparedMail, error) {
	return p.prepareNotice(request, model.MailTemplateIdentityEmailChangeWarningOld)
}

func (p *Composer) PrepareEmailVerifiedByAdministrator(request NoticePreparation) (*store.PreparedMail, error) {
	return p.prepareNotice(request, model.MailTemplateIdentityEmailVerifiedByAdmin)
}

func (p *Composer) PrepareAccountStateChanged(request NoticePreparation, enabled bool) (*store.PreparedMail, error) {
	key := model.MailTemplateIdentityAccountDisabled
	if enabled {
		key = model.MailTemplateIdentityAccountEnabled
	}
	return p.prepareNotice(request, key)
}

func (p *Composer) PrepareSessionsRevokedByAdministrator(request NoticePreparation) (*store.PreparedMail, error) {
	return p.prepareNotice(request, model.MailTemplateIdentitySessionsRevokedByAdmin)
}

func (p *Composer) PrepareMFANotice(request NoticePreparation, kind MFANoticeKind) (*store.PreparedMail, error) {
	var key model.MailTemplateKey
	switch kind {
	case MFANoticeEnabled:
		key = model.MailTemplateIdentityMFAEnabled
	case MFANoticeDisabled:
		key = model.MailTemplateIdentityMFADisabled
	case MFANoticeRecoveryCodesRegenerated:
		key = model.MailTemplateIdentityMFARecoveryCodesRegenerated
	default:
		return nil, errors.New("MFA mail notice kind is invalid")
	}
	return p.prepareNotice(request, key)
}

func (p *Composer) PrepareInvitationAccepted(request NoticePreparation) (*store.PreparedMail, error) {
	return p.prepareNotice(request, model.MailTemplateAccessInvitationAccepted)
}

func (p *Composer) PrepareOperatorTest(request NoticePreparation) (*store.PreparedMail, error) {
	return p.prepareNotice(request, model.MailTemplateSystemTest)
}

func (p *Composer) prepareNotice(request NoticePreparation, key model.MailTemplateKey) (*store.PreparedMail, error) {
	definition, ok := definitionFor(key)
	if !ok || definition.defaultLifetime <= 0 || definition.jobType != model.JobTypeMailDeliver || definition.actionRequired {
		return nil, errors.New("mail notice definition is invalid")
	}
	return p.prepareDirect(directPreparation{Recipient: request.Recipient, OccurrenceID: model.NewMailOccurrenceID(),
		TemplateKey: key, At: request.At, Deadline: request.At.Add(definition.defaultLifetime)})
}

func (p *Composer) PrepareInvitation(invitation *model.Invitation, actionURL string) (*store.PreparedMail, error) {
	if !p.Enabled() || invitation == nil || invitation.Validate() != nil || invitation.State != model.InvitationPending {
		return nil, errors.New("invitation mail input is invalid")
	}
	key, err := InvitationTemplateKey(invitation.Purpose)
	if err != nil {
		return nil, err
	}
	return p.prepareRecipient(invitation.Suggestions.DisplayName, invitation.TargetEmail, invitation.Suggestions.Locale,
		invitation.InviterUserID, "", invitation.ID, model.MailOccurrenceID(invitation.ID.String()),
		key, actionURL, invitation.CreatedAt, invitation.ExpiresAt, nil)
}

func (p *Composer) PrepareInvitationResend(invitation *model.Invitation, actionURL string, actor model.UserID, at time.Time) (*store.PreparedMail, error) {
	if !p.Enabled() || invitation == nil || invitation.Validate() != nil || invitation.State != model.InvitationPending ||
		!actor.IsValid() || at.IsZero() || !model.TimeUTC(at).Before(invitation.ExpiresAt) {
		return nil, errors.New("invitation resend mail input is invalid")
	}
	key, err := InvitationTemplateKey(invitation.Purpose)
	if err != nil {
		return nil, err
	}
	return p.prepareRecipient(invitation.Suggestions.DisplayName, invitation.TargetEmail, invitation.Suggestions.Locale,
		actor, "", invitation.ID, model.NewMailOccurrenceID(), key, actionURL, at, invitation.ExpiresAt, nil)
}

func (p *Composer) PrepareInvitationRevocation(invitation *model.Invitation, actor model.UserID, at time.Time) (*store.PreparedMail, error) {
	if p == nil || invitation == nil || invitation.Validate() != nil || invitation.State != model.InvitationPending || !actor.IsValid() || at.IsZero() {
		return nil, errors.New("invitation revocation mail input is invalid")
	}
	lifetime, ok := defaultLifetimeFor(model.MailTemplateAccessInvitationRevoked)
	if !ok {
		return nil, errors.New("invitation revocation mail definition is invalid")
	}
	return p.prepareRecipient(invitation.Suggestions.DisplayName, invitation.TargetEmail, invitation.Suggestions.Locale,
		actor, "", invitation.ID, model.NewMailOccurrenceID(), model.MailTemplateAccessInvitationRevoked,
		"", at, model.TimeUTC(at).Add(lifetime), nil)
}

func InvitationTemplateKey(purpose model.InvitationPurpose) (model.MailTemplateKey, error) {
	switch purpose {
	case model.InvitationPurposeStudentClass:
		return model.MailTemplateAccessStudentClassInvitation, nil
	case model.InvitationPurposeTeacherAcademicUnit:
		return model.MailTemplateAccessTeacherAcademicUnitInvitation, nil
	case model.InvitationPurposeAcademicUnitRole:
		return model.MailTemplateAccessAcademicUnitRoleInvitation, nil
	case model.InvitationPurposeInstitutionRole:
		return model.MailTemplateAccessInstitutionRoleInvitation, nil
	default:
		return "", errors.New("invitation mail purpose is not implemented")
	}
}

func (p *Composer) PreparePersonalAccessTokenSecurityNotice(request PersonalAccessTokenPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.Recipient.IsActive() || !isPATTemplate(request.TemplateKey) {
		return nil, errors.New("personal access token mail template is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	lifetime, ok := defaultLifetimeFor(request.TemplateKey)
	if !ok {
		return nil, errors.New("personal access token mail definition is invalid")
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", model.NewMailOccurrenceID(), request.TemplateKey,
		"", at, at.Add(lifetime),
		PersonalAccessTokenDetails{Description: request.Description, ExpiresAt: request.ExpiresAt, ActionAt: at,
			ActionCount: request.ActionCount, AcademicUnitScoped: request.AcademicUnitScoped})
}

func isPATTemplate(key model.MailTemplateKey) bool {
	definition, ok := definitionFor(key)
	return ok && definition.presentation == presentationPersonalAccessToken
}

func (p *Composer) PrepareClassTransition(request ClassTransitionPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!validClassTransition(request.TemplateKey, request.Details) || request.ActionAt.IsZero() {
		return nil, errors.New("class transition mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	lifetime, ok := defaultLifetimeFor(request.TemplateKey)
	if !ok {
		return nil, errors.New("class transition mail definition is invalid")
	}
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			request.TemplateKey, at, at.Add(lifetime), model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
		request.TemplateKey, "", at, at.Add(lifetime), request.Details)
}

func validClassTransition(key model.MailTemplateKey, value ClassTransitionDetails) bool {
	if value.ClassDisplayName == "" || value.StartsAt.IsZero() || !value.EndsAt.IsZero() && !value.StartsAt.Before(value.EndsAt) {
		return false
	}
	switch key {
	case model.MailTemplateAcademicClassEnrolled:
		return value.PreviousClassDisplayName == ""
	case model.MailTemplateAcademicClassEnrollmentEnded:
		return value.PreviousClassDisplayName == "" && !value.EndsAt.IsZero()
	case model.MailTemplateAcademicClassTransferred:
		return value.PreviousClassDisplayName != ""
	default:
		return false
	}
}

func (p *Composer) PrepareRelationshipTransition(request RelationshipTransitionPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!isRelationshipTemplate(request.TemplateKey) || request.ActionAt.IsZero() {
		return nil, errors.New("relationship transition mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	lifetime, ok := defaultLifetimeFor(request.TemplateKey)
	if !ok {
		return nil, errors.New("relationship transition mail definition is invalid")
	}
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			request.TemplateKey, at, at.Add(lifetime), model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
		request.TemplateKey, "", at, at.Add(lifetime), nil)
}

func isRelationshipTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateAcademicUnitAssigned, model.MailTemplateAcademicUnitAssignmentEnded,
		model.MailTemplateAuthorizationScopedRoleAssigned, model.MailTemplateAuthorizationScopedRoleEnded,
		model.MailTemplateAuthorizationInstitutionRoleAssigned, model.MailTemplateAuthorizationInstitutionRoleEnded:
		return true
	default:
		return false
	}
}

func (p *Composer) PrepareManagerMail(request examengine.ManagerMailPreparation) (*store.ExamManagerMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!validManagerMeaning(request.TemplateKey, request.Relationship) || request.ActionAt.IsZero() {
		return nil, errors.New("exam manager mail input is invalid")
	}
	lifetime, ok := defaultLifetimeFor(request.TemplateKey)
	if !ok {
		return nil, errors.New("exam manager mail definition is invalid")
	}
	prepared, err := p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
		request.TemplateKey, "", request.ActionAt, request.ActionAt.Add(lifetime),
		ExamManagerDetails{Title: request.ExamTitle, Relationship: string(request.Relationship), ActionAt: request.ActionAt})
	if err != nil {
		return nil, err
	}
	return &store.ExamManagerMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job}, nil
}

func validManagerMeaning(key model.MailTemplateKey, relationship examengine.ManagerMailRelationship) bool {
	switch key {
	case model.MailTemplateExamManagerAdded:
		return relationship == examengine.ManagerMailRelationshipManager
	case model.MailTemplateExamManagerRemoved:
		return relationship == examengine.ManagerMailRelationshipNoLongerManager
	case model.MailTemplateExamOwnershipTransferredToYou:
		return relationship == examengine.ManagerMailRelationshipOwner
	case model.MailTemplateExamOwnershipTransferredFromYou:
		return relationship == examengine.ManagerMailRelationshipManager
	default:
		return false
	}
}

func (p *Composer) PrepareSubmissionReceiptMail(request SubmissionReceiptPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		request.Details.ExamTitle == "" || !request.Details.SittingID.IsValid() || !request.Details.SubmissionID.IsValid() ||
		request.Details.SealedAt.IsZero() || request.ActionAt.IsZero() ||
		request.TemplateKey != model.MailTemplateExamSubmissionReceived && request.TemplateKey != model.MailTemplateExamSubmissionAutomaticallySealed {
		return nil, errors.New("submission receipt mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	lifetime, ok := defaultLifetimeFor(request.TemplateKey)
	if !ok {
		return nil, errors.New("submission receipt mail definition is invalid")
	}
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			request.TemplateKey, at, at.Add(lifetime), model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
		request.TemplateKey, "", at, at.Add(lifetime), request.Details)
}

func (p *Composer) PrepareResultReleaseMail(request ResultReleasePreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		request.Details.ExamTitle == "" || request.Details.ReleasedAt.IsZero() || request.ReleasedAt.IsZero() ||
		!request.Details.ReleasedAt.Equal(request.ReleasedAt) {
		return nil, errors.New("result release mail input is invalid")
	}
	at := model.TimeUTC(request.ReleasedAt)
	lifetime, ok := defaultLifetimeFor(model.MailTemplateExamResultReleased)
	if !ok {
		return nil, errors.New("result release mail definition is invalid")
	}
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			model.MailTemplateExamResultReleased, at, at.Add(lifetime), model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
		model.MailTemplateExamResultReleased, "", at, at.Add(lifetime), request.Details)
}

func (p *Composer) PreparePublicRegistrationVerification(recipient *model.User, occurrenceID model.MailOccurrenceID,
	actionURL string, at, deadline time.Time,
) (*store.PreparedMail, error) {
	return p.prepareDirect(directPreparation{Recipient: recipient, OccurrenceID: occurrenceID,
		TemplateKey: model.MailTemplateIdentityVerifyEmail, ActionURL: actionURL, At: at, Deadline: deadline})
}

func (p *Composer) prepareRecipient(recipientName, recipientAddress, locale string, actorUserID, targetUserID model.UserID,
	targetInvitationID model.InvitationID, occurrenceID model.MailOccurrenceID, key model.MailTemplateKey,
	actionURL string, at, deadline time.Time, detail Presentation,
) (*store.PreparedMail, error) {
	definition, defined := definitionFor(key)
	if p == nil || p.sender == nil || p.renderer == nil || !model.IsValidEmail(recipientAddress) || !actorUserID.IsValid() ||
		!occurrenceID.IsValid() || targetUserID.IsValid() == targetInvitationID.IsValid() || !defined ||
		definition.actionRequired != (strings.TrimSpace(actionURL) != "") || at.IsZero() || !deadline.After(at) {
		return nil, errors.New("direct mail recipient is invalid")
	}
	at, deadline = model.TimeUTC(at), model.TimeUTC(deadline)
	if !p.sender.Enabled() {
		return p.prepareSuppressedRecipient(recipientAddress, actorUserID, targetUserID, targetInvitationID, occurrenceID,
			key, at, deadline, model.MailDeliveryDisabledCode)
	}
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return nil, err
	}
	job, err := model.NewJob(jobID, definition.jobType, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	if err != nil {
		return nil, err
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: definition.kind, TemplateKey: key, ActorUserID: actorUserID, CreatedAt: at}
	rendered, err := p.renderer.Render(RenderRequest{Key: key, Locale: locale, ActionURL: actionURL, Presentation: detail})
	if err != nil {
		return nil, err
	}
	from := p.sender.From()
	if err = ValidateAddress(from); err != nil {
		return nil, err
	}
	frozen, err := freezeDeliveryPayload(p.sealer, deliveryID, from, Address{Name: recipientName, Address: recipientAddress}, rendered)
	if err != nil {
		return nil, err
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: targetUserID,
		TargetInvitationID: targetInvitationID, TemplateKey: key, TemplateDigest: frozen.templateDigest,
		MaskedRecipient: MaskAddress(recipientAddress), State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at,
		MessageDate: at, Deadline: deadline, MessageID: frozen.messageID, EncryptedPayload: frozen.encrypted, Revision: 1}
	if err = occurrence.Validate(); err != nil {
		return nil, err
	}
	if err = delivery.Validate(); err != nil {
		return nil, err
	}
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}, nil
}

func (p *Composer) prepareSuppressedRecipient(recipientAddress string, actorUserID, targetUserID model.UserID,
	targetInvitationID model.InvitationID, occurrenceID model.MailOccurrenceID, key model.MailTemplateKey,
	at, deadline time.Time, publicCode string,
) (*store.PreparedMail, error) {
	definition, defined := definitionFor(key)
	if p == nil || p.sender == nil || !model.IsValidEmail(recipientAddress) || !actorUserID.IsValid() ||
		targetUserID.IsValid() == targetInvitationID.IsValid() || !occurrenceID.IsValid() || !defined || at.IsZero() || !deadline.After(at) ||
		publicCode != model.MailDeliveryDisabledCode && publicCode != model.MailDeliveryRecipientIneligibleCode {
		return nil, errors.New("direct mail suppression input is invalid")
	}
	at, deadline = model.TimeUTC(at), model.TimeUTC(deadline)
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return nil, err
	}
	job, err := model.NewJob(jobID, definition.jobType, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	if err != nil {
		return nil, err
	}
	job, err = job.RequestCancellation(at)
	if err != nil {
		return nil, err
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: definition.kind, TemplateKey: key, ActorUserID: actorUserID, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: targetUserID,
		TargetInvitationID: targetInvitationID, TemplateKey: key, TemplateDigest: Digest("", "", ""),
		MaskedRecipient: MaskAddress(recipientAddress), State: model.MailDeliverySuppressed, CreatedAt: at, UpdatedAt: at,
		MessageDate: at, Deadline: deadline, MessageID: StableMessageID(deliveryID, ""), PublicFailureCode: publicCode, Revision: 1}
	if err = occurrence.Validate(); err != nil {
		return nil, err
	}
	if err = delivery.Validate(); err != nil {
		return nil, err
	}
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}, nil
}

func ValidateAddress(address Address) error {
	if address.Address == "" || strings.ContainsAny(address.Name+address.Address, "\x00\r\n") {
		return errors.New("mail address is invalid")
	}
	parsed, err := mail.ParseAddress(address.Address)
	if err != nil || parsed.Address != address.Address {
		return errors.New("mail address is invalid")
	}
	return nil
}

func StableMessageID(id model.MailDeliveryID, from string) string {
	domain := "localhost"
	if parsed, err := mail.ParseAddress(from); err == nil {
		if at := strings.LastIndexByte(parsed.Address, '@'); at >= 0 {
			domain = parsed.Address[at+1:]
		}
	}
	return "<mail." + id.String() + "@" + strings.ToLower(domain) + ">"
}

func MaskAddress(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 1 {
		return "***"
	}
	local := []rune(address[:at])
	prefix := "***"
	if len(local) > 1 {
		prefix = string(local[0]) + strings.Repeat("*", min(3, len(local)-1))
	}
	return prefix + address[at:]
}

func Digest(subject, text, html string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(subject))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(text))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(html))
	return hex.EncodeToString(hash.Sum(nil))
}
