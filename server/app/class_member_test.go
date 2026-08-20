// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type classMemberStoreFake struct {
	events      *[]string
	current     *model.ClassMember
	enrollInput *store.ClassMemberEnrollment
	endInput    *store.ClassMemberEnd
	active      []*model.ClassMember
}

func (s *classMemberStoreFake) ListActiveByUser(context.Context, string, int64) ([]*model.ClassMember, error) {
	*s.events = append(*s.events, "list-active")
	return s.active, nil
}

func (s *classMemberStoreFake) Get(context.Context, string) (*model.ClassMember, error) {
	*s.events = append(*s.events, "get-member")
	return s.current, nil
}
func (s *classMemberStoreFake) ListByClass(context.Context, string, int64) ([]*model.ClassMember, error) {
	*s.events = append(*s.events, "list-members")
	return nil, nil
}
func (s *classMemberStoreFake) EnrollWithAudit(_ context.Context, input *store.ClassMemberEnrollment) (*store.ClassEnrollmentResult, error) {
	*s.events = append(*s.events, "store-enroll")
	s.enrollInput = input
	return &store.ClassEnrollmentResult{Membership: input.Member}, nil
}
func (s *classMemberStoreFake) EndWithAudit(_ context.Context, input *store.ClassMemberEnd) (*model.ClassMember, error) {
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

func (s *classMemberClassStoreFake) Get(_ context.Context, id string) (*model.Class, error) {
	*s.events = append(*s.events, "get-class")
	if s.values != nil {
		return s.values[id], nil
	}
	return s.value, nil
}

type classMemberUserStoreFake struct {
	events *[]string
	value  *model.User
}

func (s *classMemberUserStoreFake) Get(context.Context, string) (*model.User, error) {
	*s.events = append(*s.events, "get-user")
	return s.value, nil
}

type classMemberMailFake struct {
	events   *[]string
	requests []ClassTransitionMailPreparation
}

func (m *classMemberMailFake) PrepareClassTransition(request ClassTransitionMailPreparation) (*preparedDirectMail, error) {
	*m.events = append(*m.events, "prepare-mail")
	m.requests = append(m.requests, request)
	return &preparedDirectMail{Occurrence: &model.MailOccurrence{TemplateKey: request.TemplateKey},
		Delivery: &model.MailDelivery{TemplateKey: request.TemplateKey}, Job: &model.Job{}}, nil
}

func TestClassMemberEnrollUsesDestinationPeriodAndAtomicStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	classID, periodID, userID := model.NewId(), model.NewId(), model.NewId()
	persistence := &classMemberStoreFake{events: &events}
	classes := &classMemberClassStoreFake{events: &events, value: &model.Class{ID: model.ClassID(classID), AcademicPeriodID: model.AcademicPeriodID(periodID), DisplayName: "Class A"}}
	principal := model.Principal{UserID: model.NewUserID()}
	user := mailTestUser(model.Principal{UserID: model.UserID(userID)}, time.UnixMilli(500))
	users := &classMemberUserStoreFake{events: &events, value: user}
	mailer := &classMemberMailFake{events: &events}
	clockCalls := 0
	service := newClassMemberService(persistence, classes, users, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer, func() time.Time {
		clockCalls++
		return time.UnixMilli(500)
	}, model.NewId)
	enrollment, err := service.Enroll(context.Background(), NewInvocation(principal, model.RequestMetadata{}), EnrollClassMemberCommand{ClassID: classID, UserID: userID, StartAt: 100})
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.Membership.AcademicPeriodID.String() != periodID || persistence.enrollInput.Member.UserID.String() != userID {
		t.Fatalf("enrollment/input = %#v / %#v", enrollment, persistence.enrollInput)
	}
	if persistence.enrollInput.ExpectedRecipientRevision != user.Revision {
		t.Fatalf("expected recipient revision=%d, want %d", persistence.enrollInput.ExpectedRecipientRevision, user.Revision)
	}
	if clockCalls != 1 || persistence.enrollInput.AuditAt != model.MillisFromTime(enrollment.Membership.CreatedAt) {
		t.Fatalf("clock calls/creation/audit time = %d/%v/%d", clockCalls, enrollment.Membership.CreatedAt, persistence.enrollInput.AuditAt)
	}
	if len(mailer.requests) != 1 || mailer.requests[0].TemplateKey != model.MailTemplateAcademicClassEnrolled ||
		mailer.requests[0].Details.ClassDisplayName != classes.value.DisplayName || persistence.enrollInput.Notice == nil ||
		persistence.enrollInput.ExpectedPreviousID.IsValid() {
		t.Fatalf("enrollment mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	}
	want := []string{"authorize", "get-class", "list-active", "get-user", "prepare-mail", "audit-begin", "store-enroll"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMemberRetainedEnrollmentBypassesChangedMembershipAndMail(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &classMemberStoreFake{events: &events, active: []*model.ClassMember{{ID: model.NewClassMemberID(), ClassID: model.NewClassID()}}}
	mailer := &classMemberMailFake{events: &events}
	service := newClassMemberService(persistence, &classMemberClassStoreFake{events: &events}, &classMemberUserStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer,
		func() time.Time { return time.UnixMilli(500) }, func() string { return model.NewClassMemberID().String() })
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	result, err := service.Enroll(context.Background(), invocation, EnrollClassMemberCommand{ClassID: model.NewClassID().String(),
		UserID: model.NewUserID().String(), StartAt: 100, IdempotencyKey: "row", batchRetainedOutcome: true})
	if err != nil || result == nil || len(mailer.requests) != 0 || slices.Contains(events, "list-active") ||
		slices.Contains(events, "get-class") || slices.Contains(events, "get-user") || persistence.enrollInput == nil || persistence.enrollInput.Notice != nil {
		t.Fatalf("retained enrollment=%#v error=%v mail=%#v events=%v input=%#v", result, err, mailer.requests, events, persistence.enrollInput)
	}
}

func TestClassMemberAlreadySatisfiedEnrollmentPreparesNoticeForAuthoritativeRace(t *testing.T) {
	t.Parallel()
	events := []string{}
	classID, periodID, userID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	current := &model.ClassMember{ID: model.NewClassMemberID(), ClassID: classID, AcademicPeriodID: periodID, UserID: userID, StartsAt: model.TimeFromMillis(100)}
	persistence := &classMemberStoreFake{events: &events, active: []*model.ClassMember{current}}
	class := &model.Class{ID: classID, AcademicPeriodID: periodID, DisplayName: "Class", Revision: 1}
	mailer := &classMemberMailFake{events: &events}
	service := newClassMemberService(persistence, &classMemberClassStoreFake{events: &events, value: class},
		&classMemberUserStoreFake{events: &events, value: mailTestUser(model.Principal{UserID: userID}, time.UnixMilli(500))},
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer,
		func() time.Time { return time.UnixMilli(500) }, func() string { return model.NewClassMemberID().String() })
	result, err := service.Enroll(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		EnrollClassMemberCommand{ClassID: classID.String(), UserID: userID.String(), StartAt: 100, IdempotencyKey: "row"})
	if err != nil || result == nil || persistence.enrollInput == nil || persistence.enrollInput.Notice == nil || len(mailer.requests) != 1 {
		t.Fatalf("already-satisfied enrollment=%#v error=%v mail=%#v input=%#v", result, err, mailer.requests, persistence.enrollInput)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	service := newClassMemberService(persistence, classes, users, &programmeAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events, beginID: model.NewId()}, preparer,
		func() time.Time { return time.UnixMilli(500) }, model.NewId)
	if _, err = service.Enroll(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		EnrollClassMemberCommand{ClassID: classID.String(), UserID: userID.String(), StartAt: 100}); err != nil {
		t.Fatal(err)
	}
	if persistence.enrollInput == nil || persistence.enrollInput.Notice == nil ||
		persistence.enrollInput.ExpectedRecipientRevision != user.Revision ||
		persistence.enrollInput.Notice.Delivery.State != model.MailDeliverySuppressed ||
		persistence.enrollInput.Notice.Delivery.PublicFailureCode != model.MailDeliveryRecipientIneligibleCode ||
		persistence.enrollInput.Notice.Job.Status != model.JobStatusCanceled {
		t.Fatalf("inactive recipient enrollment notice=%#v", persistence.enrollInput)
	}
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
	destinationAuditID, sourceAuditID := model.NewId(), model.NewId()
	auditor := &institutionAuditorFake{events: &events, beginIDs: []string{destinationAuditID, sourceAuditID}}
	service := newClassMemberService(persistence, classes, users, &programmeAuthorizerFake{events: &events},
		auditor, mailer,
		func() time.Time { return time.UnixMilli(500) }, model.NewId)
	principal := model.Principal{UserID: model.NewUserID()}
	authority := &store.CommandAuthorization{Principal: principal, ScopeType: model.RoleScopeClass, ScopeID: destination.ID.String(),
		Actions: []model.Action{model.ActionOnboardingBatchManage, model.ActionClassMembersManage}}
	_, err := service.Enroll(context.Background(), NewInvocation(principal, model.RequestMetadata{}),
		EnrollClassMemberCommand{ClassID: destination.ID.String(), UserID: userID.String(), StartAt: 200,
			ExpectedPreviousID: previous.ID.String(), RequireTransfer: true, IdempotencyKey: "row", batchAuthorization: authority,
			batchMetadata: &store.CommandBatch{GroupDigest: [32]byte{1}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(mailer.requests) != 1 || mailer.requests[0].TemplateKey != model.MailTemplateAcademicClassTransferred ||
		mailer.requests[0].Details.PreviousClassDisplayName != source.DisplayName ||
		mailer.requests[0].Details.ClassDisplayName != destination.DisplayName ||
		persistence.enrollInput.ExpectedPreviousID != previous.ID || persistence.enrollInput.Notice == nil ||
		persistence.enrollInput.ExpectedRecipientRevision != users.value.Revision ||
		persistence.enrollInput.AuditEventID != destinationAuditID || persistence.enrollInput.PreviousAuditEventID != sourceAuditID {
		t.Fatalf("transfer mail/input = %#v / %#v", mailer.requests, persistence.enrollInput)
	}
	wantAuditResources := []model.Resource{{Type: model.ResourceClass, ID: destination.ID.String()}, {Type: model.ResourceClass, ID: source.ID.String()}}
	if !reflect.DeepEqual(auditor.resources, wantAuditResources) {
		t.Fatalf("transfer audit resources = %#v, want %#v", auditor.resources, wantAuditResources)
	}
	if persistence.enrollInput.Command == nil || persistence.enrollInput.Command.Authorization.ClassMemberID != previous.ID {
		t.Fatalf("terminal source authority = %#v", persistence.enrollInput.Command)
	}
	want := []string{"authorize", "get-class", "list-active", "authorize", "get-class", "get-user", "prepare-mail", "audit-begin", "audit-begin", "store-enroll"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMemberEndCarriesRevision(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.ClassMember{
		ID: model.NewClassMemberID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 4, ClassID: model.ClassID(model.NewId()), AcademicPeriodID: model.AcademicPeriodID(model.NewId()),
		UserID: model.UserID(model.NewId()), StartsAt: model.TimeFromMillis(100),
	}
	persistence := &classMemberStoreFake{events: &events, current: current}
	class := &model.Class{ID: current.ClassID, AcademicPeriodID: current.AcademicPeriodID, DisplayName: "Ended Class"}
	users := &classMemberUserStoreFake{events: &events, value: mailTestUser(model.Principal{UserID: current.UserID}, time.UnixMilli(500))}
	mailer := &classMemberMailFake{events: &events}
	service := newClassMemberService(persistence, &classMemberClassStoreFake{events: &events, value: class}, users,
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer,
		func() time.Time { return time.UnixMilli(500) }, model.NewId)
	ended, err := service.End(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), EndClassMemberCommand{ID: current.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.endInput.ExpectedRevision != 4 || ended.Revision != 5 {
		t.Fatalf("end input/result = %#v / %#v", persistence.endInput, ended)
	}
	if persistence.endInput.ExpectedRecipientRevision != users.value.Revision {
		t.Fatalf("end expected recipient revision=%d, want %d", persistence.endInput.ExpectedRecipientRevision, users.value.Revision)
	}
	if len(mailer.requests) != 1 || mailer.requests[0].TemplateKey != model.MailTemplateAcademicClassEnrollmentEnded ||
		persistence.endInput.Notice == nil {
		t.Fatalf("end mail/input = %#v / %#v", mailer.requests, persistence.endInput)
	}
	want := []string{"authorize-preflight", "get-member", "authorize", "get-class", "get-user", "prepare-mail", "audit-begin", "store-end"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMemberEndDenialDoesNotInspectOpaqueMemberID(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newClassMemberService(
		&classMemberStoreFake{events: &events}, &classMemberClassStoreFake{events: &events},
		&classMemberUserStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events, preflightErr: NewError("authorization.denied")},
		&institutionAuditorFake{events: &events}, &classMemberMailFake{events: &events}, time.Now, model.NewId,
	)
	if _, err := service.End(context.Background(), Invocation{}, EndClassMemberCommand{ID: model.NewId()}); !Is(err, "authorization.denied") {
		t.Fatalf("End() error = %v, want authorization.denied", err)
	}
	if want := []string{"authorize-preflight"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMemberEndConcealsCrossScopeTarget(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.ClassMember{
		ID: model.NewClassMemberID(), ClassID: model.NewClassID(), AcademicPeriodID: model.NewAcademicPeriodID(), UserID: model.NewUserID(),
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), StartsAt: model.NowUTC(), Revision: 1,
	}
	service := newClassMemberService(
		&classMemberStoreFake{events: &events, current: current}, &classMemberClassStoreFake{events: &events},
		&classMemberUserStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events, err: NewError("authorization.denied")},
		&institutionAuditorFake{events: &events}, &classMemberMailFake{events: &events}, time.Now, model.NewId,
	)
	if _, err := service.End(context.Background(), Invocation{}, EndClassMemberCommand{ID: current.ID.String()}); !Is(err, "resource.not_found") {
		t.Fatalf("End() error = %v, want concealed resource.not_found", err)
	}
}
