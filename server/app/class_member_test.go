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

type classMemberStoreFake struct {
	pageOptions store.ClassMemberPageOptions
	pageResult  *store.ClassMemberPage
	events      *[]string
	current     *model.ClassMember
	enrollInput *store.ClassMemberEnrollment
	endInput    *store.ClassMemberEnd
	active      []*model.ClassMember
	replayed    bool
	noOp        bool
}

func (s *classMemberStoreFake) ListActiveByUser(context.Context, model.UserID, int64) ([]*model.ClassMember, error) {
	*s.events = append(*s.events, "list-active")
	return s.active, nil
}

func (s *classMemberStoreFake) Get(context.Context, model.ClassMemberID) (*model.ClassMember, error) {
	*s.events = append(*s.events, "get-member")
	return s.current, nil
}
func (s *classMemberStoreFake) ListByClass(context.Context, model.ClassID, int64) ([]*model.ClassMember, error) {
	*s.events = append(*s.events, "list-members")
	return nil, nil
}
func (s *classMemberStoreFake) Enroll(_ context.Context, input *store.ClassMemberEnrollment) (*store.ClassEnrollmentResult, error) {
	*s.events = append(*s.events, "store-enroll")
	s.enrollInput = input
	input.Replayed = s.replayed
	input.NoOp = s.noOp
	return &store.ClassEnrollmentResult{Membership: input.Member}, nil
}
func (s *classMemberStoreFake) End(_ context.Context, input *store.ClassMemberEnd) (*model.ClassMember, error) {
	*s.events = append(*s.events, "store-end")
	s.endInput = input
	ended := *s.current
	ended.EndsAt = model.OptionalTimeFromMillis(input.EndAt)
	ended.Revision = input.ExpectedRevision + 1
	return &ended, nil
}

type classMemberClassStoreFake struct {
	events *[]string
	value  *model.Class
	values map[string]*model.Class
}

func (s *classMemberClassStoreFake) Get(_ context.Context, id model.ClassID) (*model.Class, error) {
	*s.events = append(*s.events, "get-class")
	if s.values != nil {
		return s.values[id.String()], nil
	}
	return s.value, nil
}

type classMemberUserStoreFake struct {
	events *[]string
	value  *model.User
}

func (s *classMemberUserStoreFake) Get(context.Context, model.UserID) (*model.User, error) {
	*s.events = append(*s.events, "get-user")
	return s.value, nil
}

type classMemberMailFake struct {
	events   *[]string
	requests []ClassTransitionMailPreparation
}

type classMemberEffectsFake struct {
	calls     int
	candidate model.UserID
	classIDs  []model.ClassID
	err       error
	reports   int
}

func (e *classMemberEffectsFake) MembershipChanged(_ context.Context, candidate model.UserID, classIDs []model.ClassID) error {
	e.calls++
	e.candidate = candidate
	e.classIDs = append([]model.ClassID(nil), classIDs...)
	return e.err
}

func (e *classMemberEffectsFake) Report(context.Context, string, error) { e.reports++ }

func (e *classMemberEffectsFake) InvalidateAuthorization(context.Context, string) {}

func (m *classMemberMailFake) PrepareClassTransition(request ClassTransitionMailPreparation) (*store.PreparedMail, error) {
	*m.events = append(*m.events, "prepare-mail")
	m.requests = append(m.requests, request)
	return &store.PreparedMail{Occurrence: &model.MailOccurrence{TemplateKey: request.TemplateKey},
		Delivery: &model.MailDelivery{TemplateKey: request.TemplateKey}, Job: &model.Job{}}, nil
}

func TestClassMemberEnrollUsesDestinationPeriodAndAtomicStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	classID, periodID, userID := model.NewID(), model.NewID(), model.NewID()
	persistence := &classMemberStoreFake{events: &events}
	classes := &classMemberClassStoreFake{events: &events, value: &model.Class{ID: model.ClassID(classID), AcademicPeriodID: model.AcademicPeriodID(periodID), DisplayName: "Class A"}}
	principal := model.Principal{UserID: model.NewUserID()}
	user := mailTestUser(model.Principal{UserID: model.UserID(userID)}, time.UnixMilli(500))
	users := &classMemberUserStoreFake{events: &events, value: user}
	mailer := &classMemberMailFake{events: &events}
	effects := &classMemberEffectsFake{}
	clockCalls := 0
	service := newClassMemberService(classMemberServiceDependencies{Store: persistence, Classes: classes, Users: users, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mailer, Effects: effects, Now: func() time.Time {
		clockCalls++
		return time.UnixMilli(500)
	}, NewID: model.NewID})
	enrollment, err := service.EnrollClassMember(context.Background(), invocation.New(principal, model.RequestMetadata{}), EnrollClassMemberCommand{ClassID: model.ClassID(classID), UserID: userID, StartAt: 100})
	require.NoError(t, err)
	require.Equal(t, periodID, enrollment.Membership.AcademicPeriodID.String(), "enrollment/input = %#v / %#v", enrollment, persistence.enrollInput)
	require.Equal(t, userID, persistence.enrollInput.Member.UserID.String(), "enrollment/input = %#v / %#v", enrollment, persistence.enrollInput)
	require.Equal(t, user.Revision, persistence.enrollInput.ExpectedRecipientRevision, "expected recipient revision")
	require.Equal(t, 1, clockCalls, "clock calls/creation/audit time")
	require.Equal(t, model.MillisFromTime(enrollment.Membership.CreatedAt), persistence.enrollInput.AuditAt, "clock calls/creation/audit time")
	require.Len(t, mailer.requests, 1, "enrollment mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, model.MailTemplateAcademicClassEnrolled, mailer.requests[0].TemplateKey, "enrollment mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, classes.value.DisplayName, mailer.requests[0].Details.ClassDisplayName, "enrollment mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.NotNil(t, persistence.enrollInput.Notice, "enrollment mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.False(t, persistence.enrollInput.ExpectedPreviousID.IsValid(), "enrollment mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize", "get-class", "list-active", "get-user", "prepare-mail", "audit-begin", "store-enroll"}, events)
	require.Equal(t, 1, effects.calls)
	require.Equal(t, model.UserID(userID), effects.candidate)
	require.Equal(t, []model.ClassID{model.ClassID(classID)}, effects.classIDs)
}

func TestClassMemberEnrollReplayDoesNotPublishMembershipChange(t *testing.T) {
	t.Parallel()
	events := []string{}
	classID, periodID, userID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	persistence := &classMemberStoreFake{events: &events, replayed: true}
	effects := &classMemberEffectsFake{}
	service := newClassMemberService(classMemberServiceDependencies{Store: persistence, Classes: &classMemberClassStoreFake{events: &events, value: &model.Class{
		ID: classID, AcademicPeriodID: periodID, DisplayName: "Class A",
	}}, Users: &classMemberUserStoreFake{events: &events, value: mailTestUser(model.Principal{UserID: userID}, time.UnixMilli(500))}, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: &classMemberMailFake{events: &events}, Effects: effects, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	result, err := service.EnrollClassMember(
		context.Background(),
		invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		EnrollClassMemberCommand{
			ClassID: classID, UserID: userID.String(), StartAt: 100, IdempotencyKey: "replay",
		},
	)
	require.NoError(t, err, "Enroll()")
	require.NotNil(t, result, "Enroll()")
	require.Equal(t, 0, effects.calls, "replayed enrollment published")
}

func TestClassMemberRetainedEnrollmentBypassesChangedMembershipAndMail(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &classMemberStoreFake{events: &events, active: []*model.ClassMember{{ID: model.NewClassMemberID(), ClassID: model.NewClassID()}}}
	mailer := &classMemberMailFake{events: &events}
	service := newClassMemberService(classMemberServiceDependencies{Store: persistence, Classes: &classMemberClassStoreFake{events: &events}, Users: &classMemberUserStoreFake{events: &events}, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mailer, Effects: &classMemberEffectsFake{}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: func() string { return model.NewClassMemberID().String() }})
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	result, err := service.EnrollClassMember(context.Background(), invocation, EnrollClassMemberCommand{ClassID: model.NewClassID(),
		UserID: model.NewUserID().String(), StartAt: 100, IdempotencyKey: "row", batchRetainedOutcome: true})
	require.NoError(t, err, "retained enrollment")
	require.NotNil(t, result, "retained enrollment")
	require.Empty(t, mailer.requests, "retained enrollment")
	require.Equal(t, []string{"authorize", "audit-begin", "store-enroll"}, events)
	require.NotNil(t, persistence.enrollInput, "retained enrollment")
	require.Nil(t, persistence.enrollInput.Notice, "retained enrollment")
}

func TestClassMemberAlreadySatisfiedEnrollmentPreparesNoticeForAuthoritativeRace(t *testing.T) {
	t.Parallel()
	events := []string{}
	classID, periodID, userID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	current := &model.ClassMember{ID: model.NewClassMemberID(), ClassID: classID, AcademicPeriodID: periodID, UserID: userID, StartsAt: model.TimeFromMillis(100)}
	persistence := &classMemberStoreFake{events: &events, active: []*model.ClassMember{current}}
	class := &model.Class{ID: classID, AcademicPeriodID: periodID, DisplayName: "Class", Revision: 1}
	mailer := &classMemberMailFake{events: &events}
	service := newClassMemberService(classMemberServiceDependencies{Store: persistence, Classes: &classMemberClassStoreFake{events: &events, value: class}, Users: &classMemberUserStoreFake{events: &events, value: mailTestUser(model.Principal{UserID: userID}, time.UnixMilli(500))}, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mailer, Effects: &classMemberEffectsFake{}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: func() string { return model.NewClassMemberID().String() }})
	result, err := service.EnrollClassMember(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		EnrollClassMemberCommand{ClassID: classID, UserID: userID.String(), StartAt: 100, IdempotencyKey: "row"})
	require.NoError(t, err, "already-satisfied enrollment")
	require.NotNil(t, result, "already-satisfied enrollment")
	require.NotNil(t, persistence.enrollInput, "already-satisfied enrollment")
	require.NotNil(t, persistence.enrollInput.Notice, "already-satisfied enrollment")
	require.Len(t, mailer.requests, 1, "already-satisfied enrollment")
}

func TestClassMemberEnrollCommitsTerminalNoticeForInactiveRecipient(t *testing.T) {
	t.Parallel()
	events := []string{}
	classID, periodID, userID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	persistence := &classMemberStoreFake{events: &events}
	classes := &classMemberClassStoreFake{events: &events, value: &model.Class{
		ID: classID, AcademicPeriodID: periodID, DisplayName: "Class A"}}
	user := mailTestUser(model.Principal{UserID: userID}, time.UnixMilli(500))
	user.DisabledAt = model.OptionalTimeFrom(time.UnixMilli(450))
	users := &classMemberUserStoreFake{events: &events, value: user}
	preparer, err := newDirectMailPreparer(&classTransitionRendererFake{}, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	require.NoError(t, err)
	service := newClassMemberService(classMemberServiceDependencies{Store: persistence, Classes: classes, Users: users, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: preparer, Effects: &classMemberEffectsFake{}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	_, err = service.EnrollClassMember(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		EnrollClassMemberCommand{ClassID: classID, UserID: userID.String(), StartAt: 100})
	require.NoError(t, err)
	require.NotNil(t, persistence.enrollInput)
	require.NotNil(t, persistence.enrollInput.Notice)
	require.Equal(t, user.Revision, persistence.enrollInput.ExpectedRecipientRevision)
	require.Equal(t, model.MailDeliverySuppressed, persistence.enrollInput.Notice.Delivery.State)
	require.Equal(t, model.MailDeliveryRecipientIneligibleCode, persistence.enrollInput.Notice.Delivery.PublicFailureCode)
	require.Equal(t, model.JobStatusCanceled, persistence.enrollInput.Notice.Job.Status)
}

func TestClassMemberTransferAuthorizesSourceAndCreatesOneTransferNotice(t *testing.T) {
	t.Parallel()
	events := []string{}
	periodID, userID := model.NewAcademicPeriodID(), model.NewUserID()
	source := &model.Class{ID: model.NewClassID(), AcademicPeriodID: periodID, DisplayName: "Source Class"}
	destination := &model.Class{ID: model.NewClassID(), AcademicPeriodID: periodID, DisplayName: "Destination Class"}
	previous := &model.ClassMember{ID: model.NewClassMemberID(), ClassID: source.ID, AcademicPeriodID: periodID,
		UserID: userID, StartsAt: model.TimeFromMillis(100), Revision: 1}
	persistence := &classMemberStoreFake{events: &events, active: []*model.ClassMember{previous}}
	classes := &classMemberClassStoreFake{events: &events, values: map[string]*model.Class{source.ID.String(): source, destination.ID.String(): destination}}
	users := &classMemberUserStoreFake{events: &events, value: mailTestUser(model.Principal{UserID: userID}, time.UnixMilli(500))}
	mailer := &classMemberMailFake{events: &events}
	destinationAuditID, sourceAuditID := model.NewID(), model.NewID()
	auditor := &institutionAuditorFake{events: &events, beginIDs: []string{destinationAuditID, sourceAuditID}}
	service := newClassMemberService(classMemberServiceDependencies{Store: persistence, Classes: classes, Users: users, Authorization: &programmeAuthorizerFake{events: &events}, Audit: auditor, Mail: mailer, Effects: &classMemberEffectsFake{}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	principal := model.Principal{UserID: model.NewUserID()}
	authority := &store.CommandAuthorization{Principal: principal, ScopeType: model.RoleScopeClass, ScopeID: destination.ID.String(),
		Actions: []model.Action{model.ActionOnboardingBatchManage, model.ActionClassMembersManage}}
	_, err := service.EnrollClassMember(context.Background(), invocation.New(principal, model.RequestMetadata{}),
		EnrollClassMemberCommand{ClassID: destination.ID, UserID: userID.String(), StartAt: 200,
			ExpectedPreviousID: previous.ID.String(), RequireTransfer: true, IdempotencyKey: "row", batchAuthorization: authority,
			batchMetadata: &store.CommandBatch{GroupDigest: [32]byte{1}}})
	require.NoError(t, err)
	require.Len(t, mailer.requests, 1, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, model.MailTemplateAcademicClassTransferred, mailer.requests[0].TemplateKey, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, source.DisplayName, mailer.requests[0].Details.PreviousClassDisplayName, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, destination.DisplayName, mailer.requests[0].Details.ClassDisplayName, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, previous.ID, persistence.enrollInput.ExpectedPreviousID, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.NotNil(t, persistence.enrollInput.Notice, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, users.value.Revision, persistence.enrollInput.ExpectedRecipientRevision, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, model.AuditEventID(destinationAuditID), persistence.enrollInput.AuditEventID, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	require.Equal(t, model.AuditEventID(sourceAuditID), persistence.enrollInput.PreviousAuditEventID, "transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	wantAuditResources := []model.Resource{{Type: model.ResourceClass, ID: destination.ID.String()}, {Type: model.ResourceClass, ID: source.ID.String()}}
	require.Equal(t, wantAuditResources, auditor.resources, "transfer audit resources")
	require.NotNil(t, persistence.enrollInput.Command)
	require.Equal(t, previous.ID, persistence.enrollInput.Command.Authorization.ClassMemberID)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize", "get-class", "list-active", "authorize", "get-class", "get-user", "prepare-mail", "audit-begin", "audit-begin", "store-enroll"}, events)
}

func TestClassMemberEndCarriesRevision(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.ClassMember{
		ID: model.NewClassMemberID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 4, ClassID: model.ClassID(model.NewID()), AcademicPeriodID: model.AcademicPeriodID(model.NewID()),
		UserID: model.UserID(model.NewID()), StartsAt: model.TimeFromMillis(100),
	}
	persistence := &classMemberStoreFake{events: &events, current: current}
	class := &model.Class{ID: current.ClassID, AcademicPeriodID: current.AcademicPeriodID, DisplayName: "Ended Class"}
	users := &classMemberUserStoreFake{events: &events, value: mailTestUser(model.Principal{UserID: current.UserID}, time.UnixMilli(500))}
	mailer := &classMemberMailFake{events: &events}
	service := newClassMemberService(classMemberServiceDependencies{Store: persistence, Classes: &classMemberClassStoreFake{events: &events, value: class}, Users: users, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mailer, Effects: &classMemberEffectsFake{}, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	ended, err := service.EndClassMember(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), EndClassMemberCommand{ID: current.ID})
	require.NoError(t, err)
	require.Equal(t, int64(4), persistence.endInput.ExpectedRevision, "end input/result = %#v / %#v", persistence.endInput, ended)
	require.Equal(t, int64(5), ended.Revision, "end input/result = %#v / %#v", persistence.endInput, ended)
	require.Equal(t, users.value.Revision, persistence.endInput.ExpectedRecipientRevision, "end expected recipient revision")
	require.Len(t, mailer.requests, 1, "end mail/input = %#v / %#v", mailer.requests, persistence.endInput)
	require.Equal(t, model.MailTemplateAcademicClassEnrollmentEnded, mailer.requests[0].TemplateKey, "end mail/input = %#v / %#v", mailer.requests, persistence.endInput)
	require.NotNil(t, persistence.endInput.Notice, "end mail/input = %#v / %#v", mailer.requests, persistence.endInput)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize-preflight", "get-member", "authorize", "get-class", "get-user", "prepare-mail", "audit-begin", "store-end"}, events)
}

func TestClassMemberEndDenialDoesNotInspectOpaqueMemberID(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newClassMemberService(classMemberServiceDependencies{Store: &classMemberStoreFake{events: &events}, Classes: &classMemberClassStoreFake{events: &events}, Users: &classMemberUserStoreFake{events: &events}, Authorization: &programmeAuthorizerFake{events: &events, preflightErr: apperror.New("authorization.denied")}, Audit: &institutionAuditorFake{events: &events}, Mail: &classMemberMailFake{events: &events}, Effects: &classMemberEffectsFake{}, Now: time.Now, NewID: model.NewID})
	_, err := service.EndClassMember(context.Background(), invocation.Invocation{}, EndClassMemberCommand{ID: model.NewClassMemberID()})
	require.True(t, apperror.Is(err, "authorization.denied"), "End() error = %v, want authorization.denied", err)
	require.Equal(t, []string{"authorize-preflight"}, events, "events")
}

func TestClassMemberEndConcealsCrossScopeTarget(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.ClassMember{
		ID: model.NewClassMemberID(), ClassID: model.NewClassID(), AcademicPeriodID: model.NewAcademicPeriodID(), UserID: model.NewUserID(),
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), StartsAt: model.NowUTC(), Revision: 1,
	}
	service := newClassMemberService(classMemberServiceDependencies{Store: &classMemberStoreFake{events: &events, current: current}, Classes: &classMemberClassStoreFake{events: &events}, Users: &classMemberUserStoreFake{events: &events}, Authorization: &programmeAuthorizerFake{events: &events, err: apperror.New("authorization.denied")}, Audit: &institutionAuditorFake{events: &events}, Mail: &classMemberMailFake{events: &events}, Effects: &classMemberEffectsFake{}, Now: time.Now, NewID: model.NewID})
	_, err := service.EndClassMember(context.Background(), invocation.Invocation{}, EndClassMemberCommand{ID: current.ID})
	require.True(t, apperror.Is(err, "resource.not_found"), "End() error = %v, want concealed resource.not_found", err)
}

func (s *classMemberStoreFake) ListPageByClass(_ context.Context, options store.ClassMemberPageOptions) (*store.ClassMemberPage, error) {
	*s.events = append(*s.events, "list-page")
	s.pageOptions = options
	if s.pageResult != nil {
		return s.pageResult, nil
	}
	return &store.ClassMemberPage{}, nil
}
