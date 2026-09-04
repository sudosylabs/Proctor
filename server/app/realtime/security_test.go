// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestSessionRevocationAppliesAllLocalEffectsBeforeStablePeerFanout(t *testing.T) {
	t.Parallel()

	order := &orderedCalls{}
	invalidator := &recordingInvalidator{order: order}
	sink := &securityRecordingSink{order: order}
	fanout := &recordingFanout{broadcast: func(string, []byte) error {
		order.add("broadcast")
		return nil
	}}
	diagnostics := &recordingDiagnostics{}
	service := mustNewService(t, invalidator, diagnostics)
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const sessionID = "cccccccccccccccccccccccccc"
	const hash = "345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"
	service.SessionsRevoked(context.Background(), userID, []string{sessionID}, []string{hash})

	wantOrder := []string{"invalidate-access", "invalidate-activity", "close-session", "broadcast"}
	if got := order.snapshot(); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("effect order = %#v, want %#v", got, wantOrder)
	}
	if len(fanout.broadcasts) != 1 || fanout.broadcasts[0].event != clusterEventSessionRevoked {
		t.Fatalf("broadcasts = %#v", fanout.broadcasts)
	}
	wantPayload := `{"user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","session_ids":["cccccccccccccccccccccccccc"],"access_token_hashes":["345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"],"close_connections":true}`
	if got := string(fanout.broadcasts[0].data); got != wantPayload {
		t.Fatalf("session revocation wire payload\n got: %s\nwant: %s", got, wantPayload)
	}

	handler := fanout.handler(clusterEventSessionRevoked)
	if handler == nil {
		t.Fatal("session revocation handler was not registered")
	}
	if err := handler(context.Background(), fanout.broadcasts[0].data); err != nil {
		t.Fatal(err)
	}
	if len(fanout.broadcasts) != 1 {
		t.Fatalf("peer session revocation rebroadcast %d events", len(fanout.broadcasts))
	}
	if invalidator.accessCalls() != 2 || invalidator.activityCalls() != 2 || sink.sessionCloseCount() != 2 {
		t.Fatalf("peer effects: access=%d activity=%d closes=%d", invalidator.accessCalls(), invalidator.activityCalls(), sink.sessionCloseCount())
	}
}

func TestAuthenticationAndAuthorizationInvalidationPreserveWireFixtures(t *testing.T) {
	t.Parallel()

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const hash = "345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"
	invalidator := &recordingInvalidator{}
	service := mustNewService(t, invalidator, &recordingDiagnostics{})
	sink := &securityRecordingSink{}
	fanout := &recordingFanout{}
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}

	service.AuthenticationCacheInvalidated(context.Background(), userID, []string{hash})
	service.InvalidateAuthorization(context.Background(), userID)
	service.InvalidateAuthorization(context.Background(), "")

	want := []peerBroadcast{
		{event: clusterEventSessionRevoked, data: []byte(`{"user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","session_ids":null,"access_token_hashes":["345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"],"close_connections":false}`)},
		{event: clusterEventAuthorizationInvalidated, data: []byte(`{"user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb"}`)},
		{event: clusterEventAuthorizationInvalidated, data: []byte(`{}`)},
	}
	if got := fanout.broadcasts; !reflect.DeepEqual(got, want) {
		t.Fatalf("security wire fixtures\n got: %#v\nwant: %#v", got, want)
	}
	if invalidator.accessCalls() != 1 || invalidator.activityCalls() != 1 {
		t.Fatalf("authentication local effects: access=%d activity=%d", invalidator.accessCalls(), invalidator.activityCalls())
	}
	if sink.userCloseCount() != 1 || sink.allCloseCount() != 1 {
		t.Fatalf("authorization local closes: user=%d all=%d", sink.userCloseCount(), sink.allCloseCount())
	}

	for _, broadcast := range append([]peerBroadcast(nil), fanout.broadcasts...) {
		handler := fanout.handler(broadcast.event)
		if handler == nil {
			t.Fatalf("handler %q was not registered", broadcast.event)
		}
		if err := handler(context.Background(), broadcast.data); err != nil {
			t.Fatalf("peer handler %q: %v", broadcast.event, err)
		}
	}
	if len(fanout.broadcasts) != 3 {
		t.Fatalf("peer security handlers rebroadcast %d events", len(fanout.broadcasts))
	}
}

func TestSecurityPropagationRejectsInvalidPeerAndLocalInputWithoutLeaks(t *testing.T) {
	t.Parallel()

	diagnostics := &recordingDiagnostics{}
	invalidator := &recordingInvalidator{}
	sink := &securityRecordingSink{}
	fanout := &recordingFanout{}
	service := mustNewService(t, invalidator, diagnostics)
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}

	const sensitive = "raw-session-or-user-value"
	service.SessionsRevoked(context.Background(), sensitive, []string{sensitive}, []string{sensitive})
	service.AuthenticationCacheInvalidated(context.Background(), sensitive, []string{sensitive})
	service.InvalidateAuthorization(context.Background(), sensitive)
	if len(fanout.broadcasts) != 0 || invalidator.accessCalls() != 0 || sink.totalCloses() != 0 {
		t.Fatalf("invalid local input produced effects: broadcasts=%d access=%d closes=%d", len(fanout.broadcasts), invalidator.accessCalls(), sink.totalCloses())
	}
	if diagnostics.count() != 3 || strings.Contains(diagnostics.joined(), sensitive) {
		t.Fatalf("diagnostics count/text = %d %q", diagnostics.count(), diagnostics.joined())
	}

	if err := fanout.handler(clusterEventSessionRevoked)(context.Background(), []byte(`{"user_id":"invalid","session_ids":[],"access_token_hashes":[],"close_connections":true}`)); err == nil {
		t.Fatal("invalid peer session revocation was accepted")
	}
	if err := fanout.handler(clusterEventAuthorizationInvalidated)(context.Background(), []byte(`{"user_id":"invalid"}`)); err == nil {
		t.Fatal("invalid peer authorization invalidation was accepted")
	}
	if len(fanout.broadcasts) != 0 || invalidator.accessCalls() != 0 || sink.totalCloses() != 0 {
		t.Fatal("invalid peer input produced effects")
	}
}

func TestSecurityPropagationMissingAndFailingFanoutIsDiagnosticOnly(t *testing.T) {
	t.Parallel()

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const sessionID = "cccccccccccccccccccccccccc"
	invalidator := &recordingInvalidator{}
	sink := &securityRecordingSink{}
	diagnostics := &recordingDiagnostics{}
	service := mustNewService(t, invalidator, diagnostics)
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	service.SessionsRevoked(context.Background(), userID, []string{sessionID}, nil)
	service.AuthenticationCacheInvalidated(context.Background(), userID, nil)
	if invalidator.accessCalls() != 2 || sink.sessionCloseCount() != 1 || diagnostics.count() != 2 {
		t.Fatalf("missing fanout effects: access=%d closes=%d diagnostics=%d", invalidator.accessCalls(), sink.sessionCloseCount(), diagnostics.count())
	}
	missingDiagnostics := diagnostics.joined()
	if !strings.Contains(missingDiagnostics, securityOperationSessionRevocation) ||
		!strings.Contains(missingDiagnostics, securityOperationAuthenticationCache) ||
		!strings.Contains(missingDiagnostics, "realtime cluster fan-out is not attached") {
		t.Fatalf("missing fanout diagnostics do not identify operation and category: %q", missingDiagnostics)
	}

	failureDiagnostics := &recordingDiagnostics{}
	serviceWithFailure := mustNewService(t, &recordingInvalidator{}, failureDiagnostics)
	secretFailure := "transport accidentally included credential-secret"
	fanout := &recordingFanout{broadcast: func(string, []byte) error { return errors.New(secretFailure) }}
	if err := serviceWithFailure.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}
	serviceWithFailure.InvalidateAuthorization(context.Background(), userID)
	failureText := failureDiagnostics.joined()
	if failureDiagnostics.count() != 1 ||
		!strings.Contains(failureText, securityOperationAuthorization) ||
		!strings.Contains(failureText, "realtime cluster fan-out failed") ||
		strings.Contains(failureText, secretFailure) {
		t.Fatalf("unsafe or incomplete failure diagnostics: %q", failureText)
	}
}

func TestSecurityPropagationWithoutSinkStillInvalidatesAndFansOut(t *testing.T) {
	t.Parallel()

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const sessionID = "cccccccccccccccccccccccccc"
	invalidator := &recordingInvalidator{}
	fanout := &recordingFanout{}
	service := mustNewService(t, invalidator, &recordingDiagnostics{})
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}

	service.SessionsRevoked(context.Background(), userID, []string{sessionID}, nil)
	service.InvalidateAuthorization(context.Background(), userID)
	if invalidator.accessCalls() != 1 || invalidator.activityCalls() != 1 {
		t.Fatalf("authentication effects: access=%d activity=%d", invalidator.accessCalls(), invalidator.activityCalls())
	}
	if len(fanout.broadcasts) != 2 {
		t.Fatalf("broadcasts = %d, want 2", len(fanout.broadcasts))
	}
}

func TestCompleteHandlerRegistrationFailureIsTerminal(t *testing.T) {
	t.Parallel()

	service := newOrdinaryTestService(t)
	registered := []string{}
	fanout := &recordingFanout{register: func(event string) error {
		registered = append(registered, event)
		if event == clusterEventAuthorizationInvalidated {
			return errors.New("registration failed")
		}
		return nil
	}}
	err := service.SetClusterFanout(fanout)
	if err == nil || !strings.Contains(err.Error(), "register authorization.invalidated cluster handler") {
		t.Fatalf("SetClusterFanout() error = %v", err)
	}
	wantRegistered := []string{clusterEventPublication, clusterEventExamAttemptUnbound,
		clusterEventSessionRevoked, clusterEventAuthorizationInvalidated}
	if !reflect.DeepEqual(registered, wantRegistered) {
		t.Fatalf("registration order = %#v, want %#v", registered, wantRegistered)
	}
	if err := service.SetClusterFanout(&recordingFanout{}); err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("retry error = %v", err)
	}
}

func TestConcurrentSecurityPropagation(t *testing.T) {
	t.Parallel()

	invalidator := &recordingInvalidator{}
	service := mustNewService(t, invalidator, &recordingDiagnostics{})
	sink := &securityRecordingSink{}
	fanout := &recordingFanout{}
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const sessionID = "cccccccccccccccccccccccccc"
	var group sync.WaitGroup
	for index := 0; index < 64; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			service.SessionsRevoked(context.Background(), userID, []string{sessionID}, nil)
		}()
	}
	group.Wait()
	if invalidator.accessCalls() != 64 || sink.sessionCloseCount() != 64 {
		t.Fatalf("concurrent local effects: access=%d closes=%d", invalidator.accessCalls(), sink.sessionCloseCount())
	}
	if len(fanout.broadcasts) != 64 {
		t.Fatalf("concurrent broadcasts = %d", len(fanout.broadcasts))
	}
}

func TestSecurityCollaboratorsAreRequiredAtConstruction(t *testing.T) {
	t.Parallel()

	if _, err := New(nil, &recordingDiagnostics{}); err == nil {
		t.Fatal("nil invalidator was accepted")
	}
	if _, err := New(&recordingInvalidator{}, nil); err == nil {
		t.Fatal("nil diagnostics were accepted")
	}
}

func mustNewService(
	t *testing.T,
	invalidator AuthenticationInvalidator,
	diagnostics Diagnostics,
) *Service {
	t.Helper()
	service, err := New(invalidator, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type recordingInvalidator struct {
	mu       sync.Mutex
	access   [][]string
	activity [][]string
	order    *orderedCalls
}

func (i *recordingInvalidator) InvalidateAccessCredentials(_ context.Context, values []string) {
	i.mu.Lock()
	i.access = append(i.access, append([]string(nil), values...))
	i.mu.Unlock()
	if i.order != nil {
		i.order.add("invalidate-access")
	}
}

func (i *recordingInvalidator) InvalidateSessionActivity(_ context.Context, values []string) {
	i.mu.Lock()
	i.activity = append(i.activity, append([]string(nil), values...))
	i.mu.Unlock()
	if i.order != nil {
		i.order.add("invalidate-activity")
	}
}

func (i *recordingInvalidator) accessCalls() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.access)
}

func (i *recordingInvalidator) activityCalls() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.activity)
}

type securityRecordingSink struct {
	mu       sync.Mutex
	sessions []string
	users    []string
	all      int
	order    *orderedCalls
}

func (*securityRecordingSink) PublishLocal(context.Context, RealtimeEvent)           {}
func (*securityRecordingSink) UnbindExamAttemptConnection(model.AttemptConnectionID) {}

func (s *securityRecordingSink) CloseSession(value string, _ ConnectionCloseReason) {
	s.mu.Lock()
	s.sessions = append(s.sessions, value)
	s.mu.Unlock()
	if s.order != nil {
		s.order.add("close-session")
	}
}

func (s *securityRecordingSink) CloseUser(value string, _ ConnectionCloseReason) {
	s.mu.Lock()
	s.users = append(s.users, value)
	s.mu.Unlock()
}

func (s *securityRecordingSink) CloseAll(ConnectionCloseReason) {
	s.mu.Lock()
	s.all++
	s.mu.Unlock()
}

func (s *securityRecordingSink) sessionCloseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *securityRecordingSink) userCloseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

func (s *securityRecordingSink) allCloseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.all
}

func (s *securityRecordingSink) totalCloses() int {
	return s.sessionCloseCount() + s.userCloseCount() + s.allCloseCount()
}

type diagnosticRecord struct {
	message string
	err     string
}

type recordingDiagnostics struct {
	mu      sync.Mutex
	records []diagnosticRecord
}

func (d *recordingDiagnostics) ErrorContext(_ context.Context, message string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, diagnosticRecord{message: message, err: fmt.Sprint(err)})
}

func (d *recordingDiagnostics) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.records)
}

func (d *recordingDiagnostics) joined() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return fmt.Sprint(d.records)
}

var _ AuthenticationInvalidator = (*recordingInvalidator)(nil)
var _ Sink = (*securityRecordingSink)(nil)
var _ Diagnostics = (*recordingDiagnostics)(nil)
