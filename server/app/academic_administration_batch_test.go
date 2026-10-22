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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type academicAdministrationBatchAuthorizerFake struct {
	resource model.Resource
	err      error
}

func (f *academicAdministrationBatchAuthorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource) error {
	if action != model.ActionOnboardingBatchManage {
		return apperror.New("authorization.denied")
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

func (f *academicAdministrationBatchCommandsFake) CreateAffiliation(_ context.Context, _ invocation.Invocation, command CreateAffiliationCommand) (*model.Affiliation, error) {
	f.operations = append(f.operations, AcademicAdministrationAffiliationAdd)
	return &model.Affiliation{ID: model.NewAffiliationID()}, nil
}
func (f *academicAdministrationBatchCommandsFake) EndAffiliation(_ context.Context, _ invocation.Invocation, command EndAffiliationCommand) (*model.Affiliation, error) {
	f.operations = append(f.operations, AcademicAdministrationAffiliationEnd)
	return &model.Affiliation{ID: model.AffiliationID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) CreateAcademicUnitMember(_ context.Context, _ invocation.Invocation, command CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	f.operations = append(f.operations, AcademicAdministrationAcademicUnitMemberAdd)
	return &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID()}, nil
}
func (f *academicAdministrationBatchCommandsFake) EndAcademicUnitMember(_ context.Context, _ invocation.Invocation, command EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	f.operations = append(f.operations, AcademicAdministrationAcademicUnitMemberEnd)
	return &model.AcademicUnitMember{ID: model.AcademicUnitMemberID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) EnrollClassMember(_ context.Context, _ invocation.Invocation, command EnrollClassMemberCommand) (*model.ClassEnrollment, error) {
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
	invocation := invocation.New(model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}, model.RequestMetadata{})

	commands := &academicAdministrationBatchCommandsFake{}
	service := newAcademicAdministrationBatchService(academicAdministrationBatchServiceDependencies{Commands: commands, Authorization: &academicAdministrationBatchAuthorizerFake{}, Outcomes: nil, Now: func() time.Time { return now }, RecentTTL: 15 * time.Minute})
	result, err := service.RunAcademicAdministrationBatch(context.Background(), invocation, RunAcademicAdministrationBatchCommand{
		Operation: AcademicAdministrationClassEnroll, ScopeType: model.RoleScopeClass, ScopeID: classID,
		IdempotencyKey: "batch", Items: []AcademicAdministrationBatchItemCommand{
			{IdempotencyKey: "b", UserID: model.UserID(userID)}, {IdempotencyKey: "a", UserID: model.UserID(userID)},
		},
	})
	require.NoError(t, err)
	require.Len(t, commands.enrollments, 2, "duplicate result = %#v, commands", result)
	require.Equal(t, "5:batch:a", commands.enrollments[0].IdempotencyKey, "duplicate result = %#v, commands", result)
	require.Equal(t, "onboarding_batch.duplicate", result.Items[0].ErrorCode, "duplicate result = %#v, commands", result)
	require.Equal(t, InvitationBatchItemSucceeded, result.Items[1].Status, "duplicate result = %#v, commands", result)
	require.Equal(t, 1, result.Succeeded, "duplicate result = %#v, commands", result)
	require.Equal(t, 1, result.Failed, "duplicate result = %#v, commands", result)
}

func TestAcademicAdministrationBatchLooksUpRetainedOutcomeBeforeDispatch(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	lookup := &academicAdministrationOutcomeLookupFake{found: true}
	service := newAcademicAdministrationBatchService(academicAdministrationBatchServiceDependencies{Commands: commands, Authorization: &academicAdministrationBatchAuthorizerFake{}, Outcomes: lookup, Now: func() time.Time { return now }, RecentTTL: 15 * time.Minute})
	invocation := invocation.New(model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}, model.RequestMetadata{})
	result, err := service.RunAcademicAdministrationBatch(context.Background(), invocation, RunAcademicAdministrationBatchCommand{Operation: AcademicAdministrationClassEnroll,
		ScopeType: model.RoleScopeClass, ScopeID: model.NewClassID().String(), IdempotencyKey: "batch",
		Items: []AcademicAdministrationBatchItemCommand{{IdempotencyKey: "row", UserID: model.NewUserID()}}})
	require.NoError(t, err, "retained lookup result=%#v command=%#v enrollments=%#v error", result, lookup.command, commands.enrollments)
	require.Equal(t, 1, result.Succeeded, "retained lookup result=%#v command=%#v enrollments=%#v error", result, lookup.command, commands.enrollments)
	require.NotNil(t, lookup.command, "retained lookup result=%#v command=%#v enrollments=%#v error", result, lookup.command, commands.enrollments)
	require.Equal(t, "class_member.enroll", lookup.command.Operation, "retained lookup result=%#v command=%#v enrollments=%#v error", result, lookup.command, commands.enrollments)
	require.Len(t, commands.enrollments, 1, "retained lookup result=%#v command=%#v enrollments=%#v error", result, lookup.command, commands.enrollments)
	require.True(t, commands.enrollments[0].batchRetainedOutcome, "retained lookup result=%#v command=%#v enrollments=%#v error", result, lookup.command, commands.enrollments)
}

func TestAcademicAdministrationBatchPreservesDistinctEffectiveHistories(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	service := newAcademicAdministrationBatchService(academicAdministrationBatchServiceDependencies{Commands: commands, Authorization: &academicAdministrationBatchAuthorizerFake{}, Outcomes: nil, Now: func() time.Time { return now }, RecentTTL: 15 * time.Minute})
	invocation := invocation.New(model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}, model.RequestMetadata{})
	userID := model.NewUserID().String()
	result, err := service.RunAcademicAdministrationBatch(context.Background(), invocation, RunAcademicAdministrationBatchCommand{Operation: AcademicAdministrationAffiliationAdd,
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), IdempotencyKey: "batch",
		Items: []AcademicAdministrationBatchItemCommand{
			{IdempotencyKey: "a", UserID: model.UserID(userID), AffiliationKind: model.AffiliationStaff, StartAt: 100, EndAt: 200},
			{IdempotencyKey: "b", UserID: model.UserID(userID), AffiliationKind: model.AffiliationStaff, StartAt: 300, EndAt: 400},
		}})
	require.NoError(t, err, "distinct histories result=%#v operations=%#v error", result, commands.operations)
	require.Equal(t, 2, result.Succeeded, "distinct histories result=%#v operations=%#v error", result, commands.operations)
	require.Equal(t, 0, result.Failed, "distinct histories result=%#v operations=%#v error", result, commands.operations)
	require.Len(t, commands.operations, 2, "distinct histories result=%#v operations=%#v error", result, commands.operations)
}
func (f *academicAdministrationBatchCommandsFake) EndClassMember(_ context.Context, _ invocation.Invocation, command EndClassMemberCommand) (*model.ClassMember, error) {
	f.operations = append(f.operations, AcademicAdministrationClassEnd)
	return &model.ClassMember{ID: model.ClassMemberID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) CreateRoleBinding(_ context.Context, _ invocation.Invocation, command CreateRoleBindingCommand) (*model.RoleBinding, error) {
	f.operations = append(f.operations, AcademicAdministrationRoleBindingCreate)
	return &model.RoleBinding{ID: model.NewRoleBindingID()}, nil
}
func (f *academicAdministrationBatchCommandsFake) EndRoleBinding(_ context.Context, _ invocation.Invocation, command EndRoleBindingCommand) (*model.RoleBinding, error) {
	f.operations = append(f.operations, AcademicAdministrationRoleBindingEnd)
	return &model.RoleBinding{ID: model.RoleBindingID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) SetUserEnabled(_ context.Context, _ invocation.Invocation, command SetUserEnabledCommand) (*model.User, error) {
	f.disabled = append(f.disabled, command)
	if command.Enabled {
		f.operations = append(f.operations, AcademicAdministrationUserEnable)
	} else {
		f.operations = append(f.operations, AcademicAdministrationUserDisable)
	}
	return &model.User{ID: model.UserID(command.ID)}, nil
}
func (f *academicAdministrationBatchCommandsFake) RevokeUserSessions(context.Context, invocation.Invocation, RevokeUserSessionsCommand) error {
	f.operations = append(f.operations, AcademicAdministrationUserSessionsRevoke)
	return nil
}

func TestAcademicAdministrationBatchRunsClosedRowsThroughOrdinaryCommands(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	authorizer := &academicAdministrationBatchAuthorizerFake{}
	service := newAcademicAdministrationBatchService(academicAdministrationBatchServiceDependencies{Commands: commands, Authorization: authorizer, Outcomes: nil, Now: func() time.Time { return now }, RecentTTL: 15 * time.Minute})
	classID := model.NewClassID().String()
	previousID := model.NewClassMemberID().String()
	userID := model.NewUserID().String()
	invocation := invocation.New(model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now)}, model.RequestMetadata{})

	result, err := service.RunAcademicAdministrationBatch(context.Background(), invocation, RunAcademicAdministrationBatchCommand{
		Operation: AcademicAdministrationClassTransfer, ScopeType: model.RoleScopeClass, ScopeID: classID,
		IdempotencyKey: "batch-key", Items: []AcademicAdministrationBatchItemCommand{
			{IdempotencyKey: "row-1", UserID: model.UserID(userID), RelationshipID: previousID},
			{IdempotencyKey: "repeated", UserID: model.NewUserID(), RelationshipID: model.NewClassMemberID().String()},
			{IdempotencyKey: "repeated", UserID: model.NewUserID(), RelationshipID: model.NewClassMemberID().String()},
		},
	})
	require.NoError(t, err)
	require.Equal(t, model.Resource{Type: model.ResourceClass, ID: classID}, authorizer.resource)
	require.Equal(t, 1, result.Succeeded, "Run() = %#v commands", result)
	require.Equal(t, 2, result.Failed, "Run() = %#v commands", result)
	require.Equal(t, 0, result.NoOp, "Run() = %#v commands", result)
	require.Len(t, commands.enrollments, 1, "Run() = %#v commands", result)
	command := commands.enrollments[0]
	require.Equal(t, model.ClassID(classID), command.ClassID)
	require.Equal(t, model.UserID(userID), command.UserID)
	require.Equal(t, model.ClassMemberID(previousID), command.ExpectedPreviousID)
	require.True(t, command.RequireTransfer)
	require.Equal(t, "9:batch-key:row-1", command.IdempotencyKey)
}

func TestAcademicAdministrationBatchDispatchesEveryClosedOperation(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	invocation := invocation.New(model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now)}, model.RequestMetadata{})
	userID, relationshipID, roleID := model.NewUserID().String(), model.NewID(), model.NewRoleID().String()
	cases := []struct {
		operation AcademicAdministrationBatchOperation
		scopeType model.RoleScopeType
		item      AcademicAdministrationBatchItemCommand
	}{
		{AcademicAdministrationAffiliationAdd, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID), AffiliationKind: model.AffiliationStaff}},
		{AcademicAdministrationAffiliationEnd, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationAcademicUnitMemberAdd, model.RoleScopeAcademicUnit, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID)}},
		{AcademicAdministrationAcademicUnitMemberEnd, model.RoleScopeAcademicUnit, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationClassEnroll, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID)}},
		{AcademicAdministrationClassEnd, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationClassTransfer, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID), RelationshipID: relationshipID}},
		{AcademicAdministrationRoleBindingCreate, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID), RoleID: model.RoleID(roleID)}},
		{AcademicAdministrationRoleBindingEnd, model.RoleScopeClass, AcademicAdministrationBatchItemCommand{RelationshipID: relationshipID}},
		{AcademicAdministrationUserEnable, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID)}},
		{AcademicAdministrationUserDisable, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID)}},
		{AcademicAdministrationUserSessionsRevoke, model.RoleScopeInstitution, AcademicAdministrationBatchItemCommand{UserID: model.UserID(userID)}},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.operation), func(t *testing.T) {
			commands := &academicAdministrationBatchCommandsFake{}
			service := newAcademicAdministrationBatchService(academicAdministrationBatchServiceDependencies{Commands: commands, Authorization: &academicAdministrationBatchAuthorizerFake{}, Outcomes: nil, Now: func() time.Time { return now }, RecentTTL: 15 * time.Minute})
			testCase.item.IdempotencyKey = "row"
			result, err := service.RunAcademicAdministrationBatch(context.Background(), invocation, RunAcademicAdministrationBatchCommand{Operation: testCase.operation,
				ScopeType: testCase.scopeType, ScopeID: model.NewID(), IdempotencyKey: "batch", Items: []AcademicAdministrationBatchItemCommand{testCase.item}})
			require.NoError(t, err, "Run(%s) = %#v, operations", testCase.operation, result)
			require.Equal(t, 1, result.Succeeded, "Run(%s) = %#v, operations", testCase.operation, result)
			require.Len(t, commands.operations, 1, "Run(%s) = %#v, operations", testCase.operation, result)
			require.Equal(t, testCase.operation, commands.operations[0], "Run(%s) = %#v, operations", testCase.operation, result)
		})
	}
}

func TestAcademicAdministrationSensitiveBatchRequiresStrongRecentInteractiveSession(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	commands := &academicAdministrationBatchCommandsFake{}
	service := newAcademicAdministrationBatchService(academicAdministrationBatchServiceDependencies{Commands: commands, Authorization: &academicAdministrationBatchAuthorizerFake{}, Outcomes: nil, Now: func() time.Time { return now }, RecentTTL: 15 * time.Minute})
	institutionID := model.NewInstitutionID().String()
	userID := model.NewUserID().String()

	_, err := service.RunAcademicAdministrationBatch(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialPersonalAccessToken}, model.RequestMetadata{}),
		RunAcademicAdministrationBatchCommand{Operation: AcademicAdministrationUserDisable, ScopeType: model.RoleScopeInstitution,
			ScopeID: institutionID, IdempotencyKey: "batch", Items: []AcademicAdministrationBatchItemCommand{{IdempotencyKey: "row", UserID: model.UserID(userID)}}})
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "PAT sensitive batch error = %v", err)
	require.Empty(t, commands.disabled)
}
