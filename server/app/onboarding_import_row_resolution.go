// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *onboardingImportService) resolveAndValidateImportRow(ctx context.Context, invocation invocation.Invocation, current *store.OnboardingImport, values map[string]string, row *store.OnboardingImportRow) error {
	if row.Timezone != "" && len(row.Timezone) > model.UserTimezoneMaxLength {
		return errors.New("timezone is too long")
	}
	if current.Mode == model.OnboardingImportAcademicAdministration {
		return s.resolveAcademicAdministrationImportRow(ctx, invocation, current, values, row)
	}
	if current.Mode == model.OnboardingImportStudentClass {
		class, err := s.deps.Classes.Get(ctx, model.ClassID(current.ScopeID))
		if err != nil {
			return invitationStoreErrors.Translate(err)
		}
		period, err := s.deps.Periods.Get(ctx, class.AcademicPeriodID)
		if err != nil {
			return invitationStoreErrors.Translate(err)
		}
		row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision = string(InvitationBatchStudentClassCreate), model.RoleScopeClass, class.ID.String(), class.Revision
		return validateImportStudentCandidate(current.ActorUserID, class, period, row)
	}
	if current.Mode == model.OnboardingImportTeacherAcademicUnit {
		unit, err := s.deps.Units.Get(ctx, model.AcademicUnitID(current.ScopeID))
		if err != nil {
			return invitationStoreErrors.Translate(err)
		}
		role, err := s.deps.Roles.Get(ctx, current.RoleID)
		if err != nil {
			return invitationStoreErrors.Translate(err)
		}
		row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision, row.RoleID, row.RoleRevision = string(InvitationBatchTeacherAcademicUnitCreate), model.RoleScopeAcademicUnit, unit.ID.String(), unit.Revision, role.ID, role.UpdatedAt.UnixMicro()
		return validateImportTeacherCandidate(current.ActorUserID, unit, role, row)
	}
	return s.resolveInstitutionImportRow(ctx, invocation, current, values, row)
}

func (s *onboardingImportService) resolveAcademicAdministrationImportRow(ctx context.Context, invocation invocation.Invocation, current *store.OnboardingImport, values map[string]string, row *store.OnboardingImportRow) error {
	operation := AcademicAdministrationBatchOperation(strings.ToLower(strings.TrimSpace(values["operation"])))
	item := AcademicAdministrationBatchItemCommand{IdempotencyKey: strconv.Itoa(row.RowNumber), UserID: row.UserID,
		RelationshipID: row.RelationshipID, RoleID: row.RoleID, AffiliationKind: row.AffiliationKind, StartAt: row.StartsAt.Millis(), EndAt: row.EndsAt.Millis()}
	if !operation.IsValid() || !academicAdministrationBatchScopeMatches(operation, current.ScopeType) || validateAcademicAdministrationBatchItem(operation, item) != nil {
		return apperror.New("request.invalid")
	}
	row.Operation, row.ScopeType, row.ScopeID = string(operation), current.ScopeType, current.ScopeID
	resource, revision, err := s.academicAdministrationImportScope(ctx, current.ScopeType, current.ScopeID)
	if err != nil {
		return err
	}
	row.TargetRevision = revision

	if operation.requiresStrongRecentSession() {
		if err = requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.AdministrationBatches.deps.RecentTTL); err != nil {
			return err
		}
	}
	return s.resolveAcademicAdministrationImportOperation(ctx, invocation, current, operation, item, resource, row)
}

// resolveAcademicAdministrationImportOperation resolves and authorizes one validated Academic Administration operation row.
func (s *onboardingImportService) resolveAcademicAdministrationImportOperation(ctx context.Context, invocation invocation.Invocation, current *store.OnboardingImport, operation AcademicAdministrationBatchOperation, item AcademicAdministrationBatchItemCommand, resource model.Resource, row *store.OnboardingImportRow) error {
	switch operation {
	case AcademicAdministrationAffiliationAdd:
		if err := s.loadAcademicAdministrationImportUser(ctx, item.UserID.String(), row); err != nil {
			return err
		}
		if !row.StartsAt.Valid {
			row.StartsAt = model.OptionalTimeFrom(current.CreatedAt)
		}
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: row.UserID.String()})
	case AcademicAdministrationAcademicUnitMemberAdd:
		if err := s.loadAcademicAdministrationImportUser(ctx, item.UserID.String(), row); err != nil {
			return err
		}
		if !row.StartsAt.Valid {
			row.StartsAt = model.OptionalTimeFrom(current.CreatedAt)
		}
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionAcademicUnitMembersManage, resource)
	case AcademicAdministrationClassEnroll:
		if err := s.loadAcademicAdministrationImportUser(ctx, item.UserID.String(), row); err != nil {
			return err
		}
		if !row.StartsAt.Valid {
			row.StartsAt = model.OptionalTimeFrom(current.CreatedAt)
		}
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassMembersManage, resource)
	case AcademicAdministrationClassTransfer:
		return s.resolveAcademicAdministrationImportClassTransfer(ctx, invocation, current, item, resource, row)
	case AcademicAdministrationRoleBindingCreate:
		return s.resolveAcademicAdministrationImportRoleBindingCreate(ctx, invocation, current, item, resource, row)
	case AcademicAdministrationAffiliationEnd, AcademicAdministrationAcademicUnitMemberEnd, AcademicAdministrationClassEnd, AcademicAdministrationRoleBindingEnd:
		return s.resolveAcademicAdministrationImportRelationshipEnd(ctx, invocation, current, operation, item, resource, row)
	case AcademicAdministrationUserEnable, AcademicAdministrationUserDisable:
		if err := s.loadAcademicAdministrationImportUser(ctx, item.UserID.String(), row); err != nil {
			return err
		}
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: row.UserID.String()})
	case AcademicAdministrationUserSessionsRevoke:
		if err := s.loadAcademicAdministrationImportUser(ctx, item.UserID.String(), row); err != nil {
			return err
		}
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionSessionManage, model.Resource{Type: model.ResourceUser, ID: row.UserID.String()})
	default:
		return apperror.New("request.invalid")
	}
}

// loadAcademicAdministrationImportUser loads the active User a row targets and records it on the row.
func (s *onboardingImportService) loadAcademicAdministrationImportUser(ctx context.Context, id string, row *store.OnboardingImportRow) error {
	user, userErr := s.deps.Users.Get(ctx, model.UserID(id))
	if userErr != nil {
		return userStoreErrors.Translate(userErr)
	}
	if user == nil || user.IsArchived() {
		return apperror.New("resource.not_found")
	}
	row.UserID = user.ID
	return nil
}

// resolveAcademicAdministrationImportClassTransfer resolves a Class transfer row and authorizes both Classes.
func (s *onboardingImportService) resolveAcademicAdministrationImportClassTransfer(ctx context.Context, invocation invocation.Invocation, current *store.OnboardingImport, item AcademicAdministrationBatchItemCommand, resource model.Resource, row *store.OnboardingImportRow) error {
	if err := s.loadAcademicAdministrationImportUser(ctx, item.UserID.String(), row); err != nil {
		return err
	}
	previous, getErr := s.deps.ClassMembers.Get(ctx, model.ClassMemberID(item.RelationshipID))
	if getErr != nil {
		return classMemberStoreErrors.Translate(getErr)
	}
	if previous.UserID != row.UserID {
		return apperror.New("resource.not_found")
	}
	if !row.StartsAt.Valid {
		row.StartsAt = model.OptionalTimeFrom(current.CreatedAt)
	}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassMembersManage, model.Resource{Type: model.ResourceClass, ID: previous.ClassID.String()}); err != nil {
		return err
	}
	return s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassMembersManage, resource)
}

// resolveAcademicAdministrationImportRoleBindingCreate resolves a Role Binding creation row and authorizes its delegation.
func (s *onboardingImportService) resolveAcademicAdministrationImportRoleBindingCreate(ctx context.Context, invocation invocation.Invocation, current *store.OnboardingImport, item AcademicAdministrationBatchItemCommand, resource model.Resource, row *store.OnboardingImportRow) error {
	if err := s.loadAcademicAdministrationImportUser(ctx, item.UserID.String(), row); err != nil {
		return err
	}
	role, roleErr := s.deps.Roles.Get(ctx, item.RoleID)
	if roleErr != nil {
		return roleBindingStoreErrors.Translate(roleErr)
	}
	row.RoleID, row.RoleRevision = role.ID, role.UpdatedAt.UnixMicro()
	if !row.StartsAt.Valid {
		row.StartsAt = model.OptionalTimeFrom(current.CreatedAt)
	}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionRoleBindingManage, resource); err != nil {
		return err
	}
	return s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, current.ScopeType, current.ScopeID)
}

// resolveAcademicAdministrationImportRelationshipEnd resolves a row that ends an existing relationship and authorizes it.
func (s *onboardingImportService) resolveAcademicAdministrationImportRelationshipEnd(ctx context.Context, invocation invocation.Invocation, current *store.OnboardingImport, operation AcademicAdministrationBatchOperation, item AcademicAdministrationBatchItemCommand, resource model.Resource, row *store.OnboardingImportRow) error {
	switch operation {
	case AcademicAdministrationAffiliationEnd:
		value, getErr := s.deps.Affiliations.Get(ctx, model.AffiliationID(item.RelationshipID))
		if getErr != nil {
			return affiliationStoreErrors.Translate(getErr)
		}
		row.UserID, row.AffiliationKind = value.UserID, value.Kind
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: value.UserID.String()})
	case AcademicAdministrationAcademicUnitMemberEnd:
		value, getErr := s.deps.UnitMembers.Get(ctx, model.AcademicUnitMemberID(item.RelationshipID))
		if getErr != nil {
			return academicUnitMemberStoreErrors.Translate(getErr)
		}
		if value.AcademicUnitID.String() != current.ScopeID {
			return apperror.New("resource.not_found")
		}
		row.UserID = value.UserID
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionAcademicUnitMembersManage, resource)
	case AcademicAdministrationClassEnd:
		value, getErr := s.deps.ClassMembers.Get(ctx, model.ClassMemberID(item.RelationshipID))
		if getErr != nil {
			return classMemberStoreErrors.Translate(getErr)
		}
		if value.ClassID.String() != current.ScopeID {
			return apperror.New("resource.not_found")
		}
		row.UserID = value.UserID
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassMembersManage, resource)
	case AcademicAdministrationRoleBindingEnd:
		value, getErr := s.deps.RoleBindings.Get(ctx, model.RoleBindingID(item.RelationshipID))
		if getErr != nil {
			return roleBindingStoreErrors.Translate(getErr)
		}
		if value.ScopeType != current.ScopeType || value.ScopeID != current.ScopeID {
			return apperror.New("resource.not_found")
		}
		row.UserID = value.UserID
		return s.deps.Authorization.Authorize(ctx, invocation, model.ActionRoleBindingManage, resource)
	default:
		return apperror.New("request.invalid")
	}
}

func (s *onboardingImportService) academicAdministrationImportScope(ctx context.Context, scopeType model.RoleScopeType, scopeID string) (model.Resource, int64, error) {
	resource, err := academicAdministrationBatchResource(scopeType, scopeID)
	if err != nil {
		return model.Resource{}, 0, err
	}
	switch scopeType {
	case model.RoleScopeInstitution:
		value, getErr := s.deps.Institutions.Get(ctx, model.InstitutionID(scopeID))
		if getErr != nil {
			return model.Resource{}, 0, invitationStoreErrors.Translate(getErr)
		}
		return resource, value.Revision, nil
	case model.RoleScopeAcademicUnit:
		value, getErr := s.deps.Units.Get(ctx, model.AcademicUnitID(scopeID))
		if getErr != nil {
			return model.Resource{}, 0, invitationStoreErrors.Translate(getErr)
		}
		return resource, value.Revision, nil
	case model.RoleScopeClass:
		value, getErr := s.deps.Classes.Get(ctx, model.ClassID(scopeID))
		if getErr != nil {
			return model.Resource{}, 0, invitationStoreErrors.Translate(getErr)
		}
		return resource, value.Revision, nil
	default:
		return model.Resource{}, 0, apperror.New("request.invalid")
	}
}

func (s *onboardingImportService) resolveInstitutionImportRow(ctx context.Context, invocation invocation.Invocation, current *store.OnboardingImport, values map[string]string, row *store.OnboardingImportRow) error {
	kind := strings.ToLower(values["kind"])
	if kind == "" {
		return apperror.New("request.invalid")
	}
	institution, err := s.deps.Institutions.Get(ctx, model.InstitutionID(current.ScopeID))
	if err != nil {
		return invitationStoreErrors.Translate(err)
	}
	if kind == "institution_role" {
		role, roleErr := s.deps.Roles.GetByName(ctx, values["role"])
		if roleErr != nil {
			return invitationStoreErrors.Translate(roleErr)
		}
		if err = s.previewRoleAuthorization(ctx, invocation, role, model.ResourceInstitution, model.RoleScopeInstitution, institution.ID.String()); err != nil {
			return err
		}
		row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision, row.RoleID, row.RoleRevision = string(InvitationBatchInstitutionRoleCreate), model.RoleScopeInstitution, institution.ID.String(), institution.Revision, role.ID, role.UpdatedAt.UnixMicro()
		return validateImportRoleCandidate(current.ActorUserID, model.InvitationPurposeInstitutionRole, model.AcademicUnitID(""), role, row)
	}
	unit, err := s.resolveExactAcademicUnit(ctx, institution.ID, values["academic_unit"])
	if err != nil {
		return err
	}
	if kind == "teacher" || kind == "academic_unit_role" {
		role, roleErr := s.deps.Roles.GetByName(ctx, values["role"])
		if roleErr != nil {
			return invitationStoreErrors.Translate(roleErr)
		}
		if kind == "teacher" {
			resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}
			for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage} {
				if err = s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
					return err
				}
			}
			if err = s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unit.ID.String()); err != nil {
				return err
			}
			row.Operation = string(InvitationBatchTeacherAcademicUnitCreate)
		} else {
			if err = s.previewRoleAuthorization(ctx, invocation, role, model.ResourceAcademicUnit, model.RoleScopeAcademicUnit, unit.ID.String()); err != nil {
				return err
			}
			row.Operation = string(InvitationBatchAcademicUnitRoleCreate)
		}
		row.ScopeType, row.ScopeID, row.TargetRevision, row.RoleID, row.RoleRevision = model.RoleScopeAcademicUnit, unit.ID.String(), unit.Revision, role.ID, role.UpdatedAt.UnixMicro()
		if kind == "teacher" {
			return validateImportTeacherCandidate(current.ActorUserID, unit, role, row)
		}
		return validateImportRoleCandidate(current.ActorUserID, model.InvitationPurposeAcademicUnitRole, unit.ID, role, row)
	}
	if kind != "student" {
		return apperror.New("request.invalid")
	}
	programme, err := s.deps.Programmes.GetByName(ctx, unit.ID, values["programme"])
	if err != nil {
		return invitationStoreErrors.Translate(err)
	}
	level, err := s.deps.Levels.GetByName(ctx, programme.ID, values["programme_level"])
	if err != nil {
		return invitationStoreErrors.Translate(err)
	}
	period, err := s.deps.Periods.GetByOwnerName(ctx, model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}, values["academic_period"])
	if store.IsNotFound(err) {
		period, err = s.deps.Periods.GetByOwnerName(ctx, model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, values["academic_period"])
	}
	if err != nil {
		return invitationStoreErrors.Translate(err)
	}
	class, err := s.deps.Classes.GetByName(ctx, level.ID, period.ID, values["class"])
	if err != nil {
		return invitationStoreErrors.Translate(err)
	}
	resource := model.Resource{Type: model.ResourceClass, ID: class.ID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage} {
		if err = s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return err
		}
	}
	row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision = string(InvitationBatchStudentClassCreate), model.RoleScopeClass, class.ID.String(), class.Revision
	return validateImportStudentCandidate(current.ActorUserID, class, period, row)
}

func (s *onboardingImportService) resolveExactAcademicUnit(ctx context.Context, institutionID model.InstitutionID, name string) (*model.AcademicUnit, error) {
	if !institutionID.IsValid() || name == "" {
		return nil, apperror.New("request.invalid")
	}
	exact, err := s.deps.Units.GetByName(ctx, institutionID, name)
	if err != nil {
		return nil, invitationStoreErrors.Translate(err)
	}
	if exact == nil || exact.IsArchived() {
		return nil, apperror.New("resource.not_found")
	}
	return exact, nil
}

func (s *onboardingImportService) previewRoleAuthorization(ctx context.Context, invocation invocation.Invocation, role *model.Role, resourceType model.ResourceType, scopeType model.RoleScopeType, scopeID string) error {
	if s.deps.Invitations != nil {
		if err := requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.Invitations.deps.RecentAuthenticationTTL); err != nil {
			return err
		}
	}
	if err := validateInvitationDelegableRole(role, scopeType); err != nil {
		return err
	}
	resource := model.Resource{Type: resourceType, ID: scopeID}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err := s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return err
		}
	}
	return s.deps.Authorization.AuthorizeActionDelegation(ctx, invocation, role.Permissions, scopeType, scopeID)
}

func validateImportStudentCandidate(actor model.UserID, class *model.Class, period *model.AcademicPeriod, row *store.OnboardingImportRow) error {
	starts := row.StartsAt.Time
	if starts.IsZero() {
		starts = period.StartsAt
	}
	candidate, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(), TargetEmail: row.Email, ClassID: class.ID, AcademicPeriodID: period.ID,
		IntendedStartsAt: starts, IntendedEndsAt: row.EndsAt, Suggestions: model.InvitationProfileSuggestions{Username: row.Username, DisplayName: row.DisplayName, FirstName: row.FirstName, LastName: row.LastName, Locale: row.Locale, Timezone: row.Timezone},
		InviterUserID: actor, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: row.UpdatedAt})
	if err == nil {
		row.Email = candidate.TargetEmail
		row.StartsAt = model.OptionalTimeFrom(candidate.IntendedStartsAt)
	}
	return err
}

func validateImportTeacherCandidate(actor model.UserID, unit *model.AcademicUnit, role *model.Role, row *store.OnboardingImportRow) error {
	starts := row.StartsAt.Time
	if starts.IsZero() {
		starts = row.UpdatedAt
	}
	candidate, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{ID: model.NewInvitationID(), TargetEmail: row.Email, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
		IntendedStartsAt: starts, IntendedEndsAt: row.EndsAt, Suggestions: model.InvitationProfileSuggestions{Username: row.Username, DisplayName: row.DisplayName, FirstName: row.FirstName, LastName: row.LastName, Locale: row.Locale, Timezone: row.Timezone},
		InviterUserID: actor, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: row.UpdatedAt})
	if err == nil {
		row.Email = candidate.TargetEmail
		row.StartsAt = model.OptionalTimeFrom(candidate.IntendedStartsAt)
	}
	return err
}

func validateImportRoleCandidate(actor model.UserID, purpose model.InvitationPurpose, unitID model.AcademicUnitID, role *model.Role, row *store.OnboardingImportRow) error {
	starts := row.StartsAt.Time
	if starts.IsZero() {
		starts = row.UpdatedAt
	}
	candidate, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose, TargetEmail: row.Email, AcademicUnitID: unitID, RoleID: role.ID,
		RoleActions: role.Permissions, IntendedStartsAt: starts, IntendedEndsAt: row.EndsAt, InviterUserID: actor, ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: row.UpdatedAt})
	if err == nil {
		row.Email = candidate.TargetEmail
		row.StartsAt = model.OptionalTimeFrom(candidate.IntendedStartsAt)
	}
	return err
}
