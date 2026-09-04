// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type academicAdministrationBatchAuthorizerFake struct {
	resource model.Resource
	err      error
}

func (f *academicAdministrationBatchAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action, resource model.Resource) error {
	if action != model.ActionOnboardingBatchManage {
		return NewError("authorization.denied")
	}
	f.resource = resource
	return f.err
}

type academicAdministrationBatchCommandsFake struct {
	enrollments []EnrollClassMemberCommand
	disabled    []SetUserEnabledCommand
	operations  []AcademicAdministrationBatchOperation
}

type academicAdministrationOutcomeLookupFake struct {
	found   bool
	command *store.CommandIdempotency
}

func (f *academicAdministrationOutcomeLookupFake) Has(_ context.Context, command *store.CommandIdempotency) (bool, error) {
	f.command = command
	return f.found, nil
}

func (f *academicAdministrationBatchCommandsFake) CreateAffiliation(_ context.Context, _ Invocation, command CreateAffiliationCommand) (*model.Affiliation, error) {
	f.operations = append(f.operations, AcademicAdministrationAffiliationAdd)
	return &model.Affiliation{ID: model.NewAffiliationID()}, nil
}
func (f *academicAdministrationBatchCommandsFake) EndAffiliation(_ context.Context, _ Invocation, command EndAffiliationCommand) (*model.Affiliation, error) {
	f.operations = append(f.operations, AcademicAdministrationAffiliationEnd)
	return &model.Affiliation{ID: model.AffiliationID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) CreateAcademicUnitMember(_ context.Context, _ Invocation, command CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	f.operations = append(f.operations, AcademicAdministrationAcademicUnitMemberAdd)
	return &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID()}, nil
}
func (f *academicAdministrationBatchCommandsFake) EndAcademicUnitMember(_ context.Context, _ Invocation, command EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	f.operations = append(f.operations, AcademicAdministrationAcademicUnitMemberEnd)
	return &model.AcademicUnitMember{ID: model.AcademicUnitMemberID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) EnrollClassMember(_ context.Context, _ Invocation, command EnrollClassMemberCommand) (*model.ClassEnrollment, error) {
	f.enrollments = append(f.enrollments, command)
	if command.batchMetadata != nil && command.batchMetadata.DuplicateOfKeyDigest != ([32]byte{}) {
		command.batchMetadata.Duplicate = true
	}
	operation := AcademicAdministrationClassEnroll
	if command.RequireTransfer {
		operation = AcademicAdministrationClassTransfer
	}
	f.operations = append(f.operations, operation)
	return &model.ClassEnrollment{Membership: &model.ClassMember{ID: model.NewClassMemberID()}}, nil
}

func TestAcademicAdministrationBatchRetainsStableSemanticDuplicateDisposition(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	userID, classID := model.NewUserID().String(), model.NewClassID().String()
	invocation := Invocation{principal: model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}}

	commands := &academicAdministrationBatchCommandsFake{}
	service := newAcademicAdministrationBatchService(commands, &academicAdministrationBatchAuthorizerFake{}, nil, func() time.Time { return now }, 15*time.Minute)
	result, err := service.Run(context.Background(), invocation, RunAcademicAdministrationBatchCommand{
		Operation: AcademicAdministrationClassEnroll, ScopeType: model.RoleScopeClass, ScopeID: classID,
		IdempotencyKey: "batch", Items: []AcademicAdministrationBatchItemCommand{
			{IdempotencyKey: "b", UserID: userID}, {IdempotencyKey: "a", UserID: userID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands.enrollments) != 2 || commands.enrollments[0].IdempotencyKey != "5:batch:a" ||
		result.Items[0].ErrorCode != "onboarding_batch.duplicate" || result.Items[1].Status != InvitationBatchItemSucceeded ||
		result.Succeeded != 1 || result.Failed != 1 {
		t.Fatalf("duplicate result = %#v, commands=%#v", result, commands.enrollments)
	}
}

func TestAcademicAdministrationBatchLooksUpRetainedOutcomeBeforeDispatch(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	lookup := &academicAdministrationOutcomeLookupFake{found: true}
	service := newAcademicAdministrationBatchService(commands, &academicAdministrationBatchAuthorizerFake{}, lookup, func() time.Time { return now }, 15*time.Minute)
	invocation := Invocation{principal: model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}}
	result, err := service.Run(context.Background(), invocation, RunAcademicAdministrationBatchCommand{Operation: AcademicAdministrationClassEnroll,
		ScopeType: model.RoleScopeClass, ScopeID: model.NewClassID().String(), IdempotencyKey: "batch",
		Items: []AcademicAdministrationBatchItemCommand{{IdempotencyKey: "row", UserID: model.NewUserID().String()}}})
	if err != nil || result.Succeeded != 1 || lookup.command == nil || lookup.command.Operation != "class_member.enroll.v1" ||
		len(commands.enrollments) != 1 || !commands.enrollments[0].batchRetainedOutcome {
		t.Fatalf("retained lookup result=%#v command=%#v enrollments=%#v error=%v", result, lookup.command, commands.enrollments, err)
	}
}

func TestAcademicAdministrationBatchPreservesDistinctEffectiveHistories(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	service := newAcademicAdministrationBatchService(commands, &academicAdministrationBatchAuthorizerFake{}, nil, func() time.Time { return now }, 15*time.Minute)
	invocation := Invocation{principal: model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}}
	userID := model.NewUserID().String()
	result, err := service.Run(context.Background(), invocation, RunAcademicAdministrationBatchCommand{Operation: AcademicAdministrationAffiliationAdd,
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), IdempotencyKey: "batch",
		Items: []AcademicAdministrationBatchItemCommand{
			{IdempotencyKey: "a", UserID: userID, AffiliationKind: model.AffiliationStaff, StartAt: 100, EndAt: 200},
			{IdempotencyKey: "b", UserID: userID, AffiliationKind: model.AffiliationStaff, StartAt: 300, EndAt: 400},
		}})
	if err != nil || result.Succeeded != 2 || result.Failed != 0 || len(commands.operations) != 2 {
		t.Fatalf("distinct histories result=%#v operations=%#v error=%v", result, commands.operations, err)
	}
}
func (f *academicAdministrationBatchCommandsFake) EndClassMember(_ context.Context, _ Invocation, command EndClassMemberCommand) (*model.ClassMember, error) {
	f.operations = append(f.operations, AcademicAdministrationClassEnd)
	return &model.ClassMember{ID: model.ClassMemberID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) CreateRoleBinding(_ context.Context, _ Invocation, command CreateRoleBindingCommand) (*model.RoleBinding, error) {
	f.operations = append(f.operations, AcademicAdministrationRoleBindingCreate)
	return &model.RoleBinding{ID: model.NewRoleBindingID()}, nil
}
func (f *academicAdministrationBatchCommandsFake) EndRoleBinding(_ context.Context, _ Invocation, command EndRoleBindingCommand) (*model.RoleBinding, error) {
	f.operations = append(f.operations, AcademicAdministrationRoleBindingEnd)
	return &model.RoleBinding{ID: model.RoleBindingID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) SetUserEnabled(_ context.Context, _ Invocation, command SetUserEnabledCommand) (*model.User, error) {
	f.disabled = append(f.disabled, command)
	if command.Enabled {
		f.operations = append(f.operations, AcademicAdministrationUserEnable)
	} else {
		f.operations = append(f.operations, AcademicAdministrationUserDisable)
	}
	return &model.User{ID: model.UserID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) RevokeUserSessions(context.Context, Invocation, RevokeUserSessionsCommand) error {
	f.operations = append(f.operations, AcademicAdministrationUserSessionsRevoke)
	return nil
}

func TestAcademicAdministrationBatchRunsClosedRowsThroughOrdinaryCommands(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	authorizer := &academicAdministrationBatchAuthorizerFake{}
	service := newAcademicAdministrationBatchService(commands, authorizer, nil, func() time.Time { return now }, 15*time.Minute)
	classID := model.NewClassID().String()
	previousID := model.NewClassMemberID().String()
	userID := model.NewUserID().String()
	invocation := Invocation{principal: model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now)}}

	result, err := service.Run(context.Background(), invocation, RunAcademicAdministrationBatchCommand{
		Operation: AcademicAdministrationClassTransfer, ScopeType: model.RoleScopeClass, ScopeID: classID,
		IdempotencyKey: "batch-key", Items: []AcademicAdministrationBatchItemCommand{
			{IdempotencyKey: "row-1", UserID: userID, RelationshipID: previousID},
			{IdempotencyKey: "repeated", UserID: model.NewUserID().String(), RelationshipID: model.NewClassMemberID().String()},
			{IdempotencyKey: "repeated", UserID: model.NewUserID().String(), RelationshipID: model.NewClassMemberID().String()},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if authorizer.resource != (model.Resource{Type: model.ResourceClass, ID: classID}) {
		t.Fatalf("batch authorization resource = %#v", authorizer.resource)
	}
	if result.Succeeded != 1 || result.Failed != 2 || result.NoOp != 0 || len(commands.enrollments) != 1 {
		t.Fatalf("Run() = %#v commands=%#v", result, commands.enrollments)
	}
	command := commands.enrollments[0]
	if command.ClassID != classID || command.UserID != userID || command.ExpectedPreviousID != previousID || !command.RequireTransfer || command.IdempotencyKey != "9:batch-key:row-1" {
		t.Fatalf("ordinary transfer command = %#v", command)
	}
}

func TestAcademicAdministrationBatchDispatchesEveryClosedOperation(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	invocation := Invocation{principal: model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now)}}
	userID, relationshipID, roleID := model.NewUserID().String(), model.NewId(), model.NewRoleID().String()
	cases := []struct {
		operation AcademicAdministrationBatchOperation
		scopeType model.RoleScopeType
		item      AcademicAdministrationBatchItemCommand
	}{
		{AcademicAdministrationAffiliationAdd, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: userID, AffiliationKind: model.AffiliationStaff}},
		{AcademicAdministrationAffiliationEnd, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationAcademicUnitMemberAdd, model.RoleScopeAcademicUnit, AcademicAdministrationBatchItemCommand{UserID: userID}},
		{AcademicAdministrationAcademicUnitMemberEnd, model.RoleScopeAcademicUnit, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationClassEnroll, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{UserID: userID}},
		{AcademicAdministrationClassEnd, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationClassTransfer, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{UserID: userID, RelationshipID: relationshipID}},
		{AcademicAdministrationRoleBindingCreate, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{UserID: userID, RoleID: roleID}},
		{AcademicAdministrationRoleBindingEnd, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationUserEnable, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: userID}},
		{AcademicAdministrationUserDisable, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: userID}},
		{AcademicAdministrationUserSessionsRevoke, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: userID}},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.operation), func(t *testing.T) {
			commands := &academicAdministrationBatchCommandsFake{}
			service := newAcademicAdministrationBatchService(commands, &academicAdministrationBatchAuthorizerFake{}, nil, func() time.Time { return now }, 15*time.Minute)
			testCase.item.IdempotencyKey = "row"
			result, err := service.Run(context.Background(), invocation, RunAcademicAdministrationBatchCommand{Operation: testCase.operation,
				ScopeType: testCase.scopeType, ScopeID: model.NewId(), IdempotencyKey: "batch", Items: []AcademicAdministrationBatchItemCommand{testCase.item}})
			if err != nil || result.Succeeded != 1 || len(commands.operations) != 1 || commands.operations[0] != testCase.operation {
				t.Fatalf("Run(%s) = %#v, operations=%#v, error=%v", testCase.operation, result, commands.operations, err)
			}
		})
	}
}

func TestAcademicAdministrationSensitiveBatchRequiresStrongRecentInteractiveSession(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	service := newAcademicAdministrationBatchService(commands, &academicAdministrationBatchAuthorizerFake{}, nil, func() time.Time { return now }, 15*time.Minute)
	institutionID := model.NewInstitutionID().String()
	userID := model.NewUserID().String()

	_, err := service.Run(context.Background(), Invocation{principal: model.Principal{UserID: model.NewUserID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialPersonalAccessToken}},
		RunAcademicAdministrationBatchCommand{Operation: AcademicAdministrationUserDisable, ScopeType: model.RoleScopeInstitution,
			ScopeID: institutionID, IdempotencyKey: "batch", Items: []AcademicAdministrationBatchItemCommand{{IdempotencyKey: "row", UserID: userID}}})
	if !Is(err, "authentication.invalid_token") {
		t.Fatalf("PAT sensitive batch error = %v", err)
	}
	if len(commands.disabled) != 0 {
		t.Fatalf("sensitive batch executed commands: %#v", commands.disabled)
	}
}
