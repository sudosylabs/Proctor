// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// AcceptExternalIdentity consumes the Invitation admitted by a consumed
// external login state, linking the external identity and granting what the
// Invitation's purpose promises.
func (s SQLInvitationStore) AcceptExternalIdentity(ctx context.Context, input *store.ExternalIdentityInvitationAcceptance) (*store.ExternalIdentityInvitationAcceptanceResult, error) {
	provider, err := validateExternalIdentityInvitationAcceptance(input)
	if err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "external identity Invitation acceptance", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExternalIdentityInvitationAcceptanceResult, error) {
		return acceptExternalIdentityInvitation(ctx, tx, input, provider)
	})
}

// validateExternalIdentityInvitationAcceptance checks the prepared acceptance package and returns its normalized provider.
func validateExternalIdentityInvitationAcceptance(input *store.ExternalIdentityInvitationAcceptance) (string, error) {
	if input == nil || !input.ExternalStateID.IsValid() || input.Identity == nil || !input.Identity.ID.IsZero() ||
		input.User == nil || input.AuditEvent == nil ||
		!input.AuditEvent.ID.IsZero() || !model.IsValidEmail(input.ProviderEmail) || !validAccessDeploymentCapabilities(input.Capabilities) {
		return "", store.NewErrInvalidInput("invitation", "external_identity_acceptance", nil)
	}
	provider := normalizeProviderID(input.Identity.Provider)
	if input.Identity.Provider != provider || input.Identity.Subject == "" || !input.Identity.LastSeenAt.Valid ||
		input.User.Email == "" || !input.User.EmailVerified || input.Identity.UserID != input.User.ID ||
		input.AuditEvent.ActorID != input.User.ID {
		return "", store.NewErrInvalidInput("invitation", "external_identity_acceptance", nil)
	}
	return provider, nil
}

// externalInvitationLoginState is the consumed external login state that admits an Invitation.
type externalInvitationLoginState struct {
	CreatedAt    time.Time    `db:"created_at"`
	InvitationID string       `db:"invitation_id"`
	Provider     string       `db:"provider"`
	Purpose      string       `db:"purpose"`
	ExpiresAt    time.Time    `db:"expires_at"`
	ConsumedAt   sql.NullTime `db:"consumed_at"`
}

// acceptExternalIdentityInvitation consumes the Invitation an external login admitted inside the acceptance transaction.
func acceptExternalIdentityInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, provider string) (*store.ExternalIdentityInvitationAcceptanceResult, error) {
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return nil, err
	}
	state, databaseNow, err := lockExternalInvitationLoginState(ctx, tx, input, provider)
	if err != nil {
		return nil, err
	}
	invitation, err := lockExternalInvitation(ctx, tx, input, state.InvitationID, provider, databaseNow)
	if err != nil {
		return nil, err
	}
	if err = requireExternalInvitationAdmission(ctx, tx, input, provider); err != nil {
		return nil, err
	}
	if err = lockExternalInvitationParticipants(ctx, tx, input, invitation, provider); err != nil {
		return nil, err
	}
	user, created, err := resolveExternalInvitationUser(ctx, tx, invitation, input, databaseNow)
	if err != nil {
		return nil, err
	}
	if err = requireExternalInvitationAuthenticationGeneration(ctx, tx, user.ID, state.CreatedAt); err != nil {
		return nil, err
	}
	identity, err := linkExternalInvitationIdentity(ctx, tx, input.Identity, user.ID, provider, databaseNow)
	if err != nil {
		return nil, err
	}
	result := &store.ExternalIdentityInvitationAcceptanceResult{Invitation: invitation, Identity: identity, User: user}
	if err = acceptExternalInvitationPurpose(ctx, tx, input, invitation, result, user.ID, created, databaseNow); err != nil {
		return nil, err
	}
	if err = persistExternalInvitationAcceptance(ctx, tx, invitation); err != nil {
		return nil, err
	}
	if err = suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, databaseNow); err != nil {
		return nil, err
	}
	if err = insertExternalInvitationAcceptanceNotice(ctx, tx, input, invitation, user.ID, created); err != nil {
		return nil, err
	}
	event, err := invitationAcceptanceAuditEvent(input.AuditEvent, invitation, user.ID)
	if err != nil {
		return nil, err
	}
	if _, err = insertAuditEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	result.Invitation = invitation
	return result, nil
}

// lockExternalInvitationLoginState locks the consumed external login state and checks it admits an Invitation through provider.
func lockExternalInvitationLoginState(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, provider string) (externalInvitationLoginState, time.Time, error) {
	var state externalInvitationLoginState
	if err := tx.Get(ctx, &state, `SELECT created_at,invitation_id,provider,purpose,expires_at,consumed_at FROM external_login_states WHERE id=? FOR UPDATE`, input.ExternalStateID.String()); err != nil {
		return state, time.Time{}, translateError("external_login_state", input.ExternalStateID.String(), err)
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return state, time.Time{}, err
	}
	if state.Provider != provider || state.Purpose != string(model.ExternalAuthenticationPurposeInvitationAdmission) ||
		state.InvitationID == "" || !state.ConsumedAt.Valid || !databaseNow.Before(state.ExpiresAt) {
		return state, time.Time{}, store.NewErrConflict("invitation", "invitation_external_state", nil)
	}
	return state, databaseNow, nil
}

// lockExternalInvitation locks the admitted Invitation and checks the prepared audit event and its pending window.
func lockExternalInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, invitationID, provider string, databaseNow time.Time) (*model.Invitation, error) {
	var row invitationRow
	if err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE id=? FOR UPDATE`, invitationID); err != nil {
		return nil, translateError("invitation", invitationID, err)
	}
	invitation, err := row.model()
	if err != nil {
		return nil, err
	}
	if err = validateExternalInvitationAudit(input.AuditEvent, invitation, provider); err != nil {
		return nil, err
	}
	if err = requirePendingInvitation(invitation, databaseNow); err != nil {
		return nil, err
	}
	return invitation, nil
}

// requireExternalInvitationAdmission requires the provider to be configured and the Access Policy to admit invitations through it.
func requireExternalInvitationAdmission(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, provider string) error {
	_, configured := input.Capabilities.Providers[provider]
	if !configured {
		return store.ErrAuthenticationMethodDisabled
	}
	policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
	if err != nil {
		return err
	}
	if !policy.InvitationAdmissionEnabled || policy.ProviderAdmissions[provider] != model.ProviderAdmissionInvitationRequired {
		return store.ErrAuthenticationMethodDisabled
	}
	return nil
}

// lockExternalInvitationParticipants locks the inviter, the target and provider mailboxes in order, and the provider subject.
func lockExternalInvitationParticipants(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, invitation *model.Invitation, provider string) error {
	if err := lockInvitationInviterBeforeHierarchy(ctx, tx, invitation.InviterUserID); err != nil {
		return err
	}
	mailboxes := []string{invitation.TargetEmail}
	if input.ProviderEmail != invitation.TargetEmail {
		mailboxes = append(mailboxes, input.ProviderEmail)
		slices.Sort(mailboxes)
	}
	for _, mailbox := range mailboxes {
		if err := lockInvitationMailbox(ctx, tx, mailbox); err != nil {
			return err
		}
	}
	return lockExternalIdentitySubject(ctx, tx, provider, input.Identity.Subject)
}

// requireExternalInvitationAuthenticationGeneration rejects a User awaiting MFA re-enrollment or reset after the login state began.
func requireExternalInvitationAuthenticationGeneration(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID, stateCreatedAt time.Time) error {
	recovery, recoveryErr := getMFARecoveryState(ctx, tx, userID)
	if recoveryErr != nil {
		return recoveryErr
	}
	if recovery.ReenrollmentRequired {
		return store.ErrMFAReenrollmentRequired
	}
	if recovery.ResetAt.Valid && stateCreatedAt.Before(recovery.ResetAt.Time) {
		return store.ErrAuthenticationGenerationChanged
	}
	return nil
}

// acceptExternalInvitationPurpose grants the relationships the Invitation's purpose promises and accepts it in memory.
func acceptExternalInvitationPurpose(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, invitation *model.Invitation,
	result *store.ExternalIdentityInvitationAcceptanceResult, userID model.UserID, created bool, databaseNow time.Time,
) error {
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		return acceptExternalStudentClassInvitation(ctx, tx, input, invitation, result, userID, created, databaseNow)
	case model.InvitationPurposeTeacherAcademicUnit:
		return acceptExternalTeacherAcademicUnitInvitation(ctx, tx, input, invitation, result, userID, created, databaseNow)
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		return acceptExternalScopedRoleInvitation(ctx, tx, input, invitation, result, userID, created, databaseNow)
	default:
		return store.NewErrConflict("invitation", "invitation_purpose", nil)
	}
}

// acceptExternalStudentClassInvitation grants the student Affiliation and Class membership an external acceptance promises.
func acceptExternalStudentClassInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, invitation *model.Invitation,
	result *store.ExternalIdentityInvitationAcceptanceResult, userID model.UserID, created bool, databaseNow time.Time,
) error {
	if !slices.Equal(input.RequiredActions, []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage}) ||
		input.Affiliation == nil || input.ClassMember == nil || (created && input.Notice == nil) {
		return store.NewErrInvalidInput("invitation", "external_student_package", nil)
	}
	if err := validateStudentClassInvitationPackage(ctx, tx, invitation, databaseNow); err != nil {
		return err
	}
	if err := requireStudentClassInvitationAuthority(ctx, tx, invitation.InviterUserID, invitation.ClassID, databaseNow); err != nil {
		return err
	}
	effectiveStart := invitation.EffectiveStartsAt(databaseNow)
	var err error
	result.Affiliation, err = ensureInvitationStudentAffiliation(ctx, tx, input.Affiliation, userID, effectiveStart)
	if err == nil {
		result.ClassMember, err = ensureInvitationClassMember(ctx, tx, invitation, input.ClassMember, userID, effectiveStart)
	}
	if err == nil {
		err = invitation.Accept(userID, result.Affiliation.ID, result.ClassMember.ID, invitationTransitionAt(databaseNow, invitation))
	}
	return err
}

// acceptExternalTeacherAcademicUnitInvitation grants the teacher Affiliation, membership and Role Binding an external acceptance promises.
func acceptExternalTeacherAcademicUnitInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, invitation *model.Invitation,
	result *store.ExternalIdentityInvitationAcceptanceResult, userID model.UserID, created bool, databaseNow time.Time,
) error {
	if !slices.Equal(input.RequiredActions, []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}) ||
		input.Affiliation == nil || input.AcademicUnitMember == nil || input.RoleBinding == nil || (created && input.Notice == nil) {
		return store.NewErrInvalidInput("invitation", "external_teacher_package", nil)
	}
	effectiveStart := invitation.EffectiveStartsAt(databaseNow)
	err := validateTeacherAcademicUnitInvitationPackage(ctx, tx, invitation, databaseNow)
	if err == nil {
		err = requireTeacherAcademicUnitInvitationAuthority(ctx, tx, invitation, databaseNow)
	}
	if err == nil {
		result.Affiliation, err = ensureInvitationTeacherAffiliation(ctx, tx, input.Affiliation, userID, effectiveStart)
	}
	if err == nil {
		result.AcademicUnitMember, err = ensureInvitationAcademicUnitMember(ctx, tx, invitation, input.AcademicUnitMember, userID, effectiveStart)
	}
	if err == nil {
		result.RoleBinding, err = ensureInvitationRoleBinding(ctx, tx, invitation, input.RoleBinding, userID, result.AcademicUnitMember.ID, effectiveStart)
	}
	if err == nil {
		err = invitation.AcceptTeacherAcademicUnit(userID, result.Affiliation.ID, result.AcademicUnitMember.ID, result.RoleBinding.ID, invitationTransitionAt(databaseNow, invitation))
	}
	return err
}

// acceptExternalScopedRoleInvitation grants the scoped Role Binding an existing User's external acceptance promises.
func acceptExternalScopedRoleInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance, invitation *model.Invitation,
	result *store.ExternalIdentityInvitationAcceptanceResult, userID model.UserID, created bool, databaseNow time.Time,
) error {
	if created || !slices.Equal(input.RequiredActions, []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}) ||
		input.RoleBinding == nil || input.Notice != nil {
		return store.NewErrConflict("invitation", "invitation_existing_user_required", nil)
	}
	effectiveStart := invitation.EffectiveStartsAt(databaseNow)
	err := validateScopedRoleInvitationPackage(ctx, tx, invitation, databaseNow)
	if err == nil {
		err = requireScopedRoleInvitationAuthority(ctx, tx, invitation, databaseNow)
	}
	if err == nil {
		result.RoleBinding, err = ensureScopedRoleInvitationBinding(ctx, tx, invitation, input.RoleBinding, userID, effectiveStart, databaseNow)
	}
	if err == nil {
		err = invitation.AcceptScopedRole(userID, result.RoleBinding.ID, invitationTransitionAt(databaseNow, invitation))
	}
	return err
}

// insertExternalInvitationAcceptanceNotice queues the acceptance notice for a User the external acceptance created.
func insertExternalInvitationAcceptanceNotice(ctx context.Context, tx *sqlxTxWrapper, input *store.ExternalIdentityInvitationAcceptance,
	invitation *model.Invitation, userID model.UserID, created bool,
) error {
	if !created || input.Notice == nil || (invitation.Purpose != model.InvitationPurposeStudentClass && invitation.Purpose != model.InvitationPurposeTeacherAcademicUnit) {
		return nil
	}
	payloadKeyID, keyErr := validateExternalInvitationNotice(input.Notice.Occurrence, input.Notice.Delivery, input.Notice.Job)
	if keyErr != nil {
		return keyErr
	}
	occurrence := *input.Notice.Occurrence
	occurrence.ActorUserID = invitation.InviterUserID
	delivery := *input.Notice.Delivery
	delivery.TargetUserID, delivery.TargetInvitationID = userID, ""
	if payloadKeyID != "" {
		if keyErr = requireMailPayloadPrimary(ctx, tx, payloadKeyID); keyErr != nil {
			return keyErr
		}
	}
	return insertInvitationMail(ctx, tx, &occurrence, &delivery, input.Notice.Job, payloadKeyID)
}

func resolveExternalInvitationUser(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation,
	input *store.ExternalIdentityInvitationAcceptance, at time.Time,
) (*model.User, bool, error) {
	var targetRow userRow
	err := tx.Get(ctx, &targetRow, `SELECT `+strings.Join(userSliceColumns(), ",")+` FROM users WHERE email=? AND archived_at IS NULL FOR UPDATE`, invitation.TargetEmail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	var target *model.User
	if err == nil {
		target, err = targetRow.model()
		if err != nil {
			return nil, false, err
		}
		if !target.IsActive() {
			return nil, false, store.NewErrConflict("invitation", "invitation_user_disabled", nil)
		}
	}
	if input.ProviderEmail != invitation.TargetEmail {
		var providerOwner string
		ownerErr := tx.Get(ctx, &providerOwner, `SELECT id FROM users WHERE email=? AND archived_at IS NULL FOR UPDATE`, input.ProviderEmail)
		if ownerErr == nil && (target == nil || providerOwner != target.ID.String()) {
			return nil, false, store.NewErrConflict("invitation", "invitation_provider_mailbox_owned", nil)
		}
		if ownerErr != nil && !errors.Is(ownerErr, sql.ErrNoRows) {
			return nil, false, ownerErr
		}
	}
	if target != nil {
		if !target.EmailVerified {
			revision, revisionErr := advanceUserMailEligibilityRevision(ctx, tx)
			if revisionErr != nil {
				return nil, false, revisionErr
			}
			// at is PostgreSQL time while the User may carry a leading node clock.
			if _, updateErr := tx.Exec(ctx, `UPDATE users SET email_verified=TRUE,mail_eligibility_revision=?,updated_at=GREATEST(updated_at,?),revision=revision+1 WHERE id=?`, revision, at, target.ID.String()); updateErr != nil {
				return nil, false, updateErr
			}
			target.EmailVerified, target.UpdatedAt, target.Revision = true, latestTime(target.UpdatedAt, at), target.Revision+1
		}
		return target, false, nil
	}
	if invitation.Purpose == model.InvitationPurposeAcademicUnitRole || invitation.Purpose == model.InvitationPurposeInstitutionRole {
		return nil, false, store.NewErrConflict("invitation", "invitation_existing_user_required", nil)
	}
	user := *input.User
	if user.Email != invitation.TargetEmail || user.Validate() != nil || validateInitialUserSettingsDocument(&user, input.Settings) != nil ||
		validateUserDefaultProfilePictureJob(&user, input.DefaultProfilePictureJob) != nil {
		return nil, false, store.NewErrInvalidInput("invitation", "external_user", nil)
	}
	if err = insertUser(ctx, tx, &user); err != nil {
		return nil, false, err
	}
	if err = insertUserSettingsDocument(ctx, tx, input.Settings); err != nil {
		return nil, false, err
	}
	if _, err = insertQueuedJob(ctx, tx, input.DefaultProfilePictureJob, false); err != nil {
		return nil, false, err
	}
	return &user, true, nil
}

func linkExternalInvitationIdentity(ctx context.Context, tx *sqlxTxWrapper, prepared *model.ExternalIdentity,
	userID model.UserID, provider string, at time.Time,
) (*model.ExternalIdentity, error) {
	identity, _, err := resolveExternalIdentity(ctx, tx, provider, prepared.Subject, at)
	if err == nil {
		if identity.UserID != userID {
			return nil, store.NewErrConflict("external_identity", "external_identities_provider_subject_key", nil)
		}
		return identity, nil
	}
	if !store.IsNotFound(err) {
		return nil, err
	}
	candidate := *prepared
	candidate.UserID, candidate.Provider = userID, provider
	candidate.PrepareCreate(model.NewExternalIdentityID(), at)
	if err = candidate.Validate(); err != nil {
		return nil, err
	}
	if err = insertExternalIdentity(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func persistExternalInvitationAcceptance(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation) error {
	result, err := tx.Exec(ctx, `UPDATE invitations SET state=?,accepted_at=?,accepted_user_id=?,accepted_affiliation_id=?,accepted_class_member_id=?,accepted_academic_unit_member_id=?,accepted_role_binding_id=?,updated_at=?,revision=? WHERE id=? AND state='pending' AND revision=?`,
		invitation.State, optionalTimeValue(invitation.AcceptedAt), nullableID(invitation.AcceptedUserID.String()),
		nullableID(invitation.AcceptedAffiliationID.String()), nullableID(invitation.AcceptedClassMemberID.String()),
		nullableID(invitation.AcceptedAcademicUnitMemberID.String()), nullableID(invitation.AcceptedRoleBindingID.String()),
		invitation.UpdatedAt, invitation.Revision, invitation.ID.String(), invitation.Revision-1)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return store.NewErrConflict("invitation", "invitation_revision", err)
	}
	return nil
}

func validateExternalInvitationNotice(occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job) (string, error) {
	if occurrence == nil || delivery == nil || job == nil ||
		occurrence.Kind != model.MailOccurrenceInvitation || occurrence.TemplateKey != model.MailTemplateAccessInvitationAccepted ||
		!delivery.TargetUserID.IsValid() || delivery.TargetInvitationID.IsValid() || job.Type != model.JobTypeMailDeliver {
		return "", store.NewErrInvalidInput("invitation", "external_acceptance_notice", nil)
	}
	validationOccurrence := *occurrence
	validationOccurrence.ActorUserID = delivery.TargetUserID
	if err := validateRecoveryMail(&validationOccurrence, delivery, job); err != nil {
		return "", err
	}
	return mailPayloadKeyID(delivery.EncryptedPayload)
}

func validateExternalInvitationAudit(event *model.AuditEvent, invitation *model.Invitation, provider string) error {
	if event == nil || invitation == nil || event.Action != string(model.ActionInvitationAccept) || event.Status != model.AuditStatusSuccess ||
		event.ScopeType != invitation.ScopeType || event.ScopeID != invitation.ScopeID ||
		event.ClientType != string(model.SessionClientWeb) || event.AuthMethod == "" {
		return store.NewErrInvalidInput("invitation", "external_acceptance_audit", nil)
	}
	parameters := map[string]string{}
	if err := json.Unmarshal(event.Parameters, &parameters); err != nil || len(parameters) != 1 || parameters["provider"] != provider {
		return store.NewErrInvalidInput("invitation", "external_acceptance_audit", err)
	}
	want := model.Resource{}
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		want = model.Resource{Type: model.ResourceClass, ID: invitation.ClassID.String()}
	case model.InvitationPurposeTeacherAcademicUnit, model.InvitationPurposeAcademicUnitRole:
		want = model.Resource{Type: model.ResourceAcademicUnit, ID: invitation.AcademicUnitID.String()}
	case model.InvitationPurposeInstitutionRole:
		want = model.Resource{Type: model.ResourceInstitution, ID: invitation.ScopeID}
	default:
		return store.NewErrInvalidInput("invitation", "external_acceptance_audit", nil)
	}
	if event.Resource != want {
		return store.NewErrInvalidInput("invitation", "external_acceptance_audit", nil)
	}
	return nil
}
