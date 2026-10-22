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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const sessionID = "cccccccccccccccccccccccccc"
	const hash = "345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"
	service.SessionsRevoked(context.Background(), userID, []string{sessionID}, []string{hash})

	// Contract: every local effect applies before the peer fan-out; the local
	// effects may apply in any order.
	effects := order.snapshot()
	require.NotEmpty(t, effects)
	require.Equal(t, "broadcast", effects[len(effects)-1], "peer fan-out preceded a local effect")
	require.ElementsMatch(t, []string{"invalidate-access", "invalidate-activity", "close-session"}, effects[:len(effects)-1])
	require.Len(t, fanout.broadcasts, 1)
	require.Equal(t, clusterEventSessionRevoked, fanout.broadcasts[0].event)
	wantPayload := `{"user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","session_ids":["cccccccccccccccccccccccccc"],"access_token_hashes":["345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"],"close_connections":true}`
	got := string(fanout.broadcasts[0].data)
	require.Equal(t, wantPayload, got, "session revocation wire payload\n got")

	handler := fanout.handler(clusterEventSessionRevoked)
	require.NotNil(t, handler, "session revocation handler was not registered")
	require.NoError(t, handler(context.Background(), fanout.broadcasts[0].data))
	require.Len(t, fanout.broadcasts, 1, "peer session revocation rebroadcast")
	require.Equal(t, 2, invalidator.accessCalls(), "peer effects: access")
	require.Equal(t, 2, invalidator.activityCalls(), "peer effects: access")
	require.Equal(t, 2, sink.sessionCloseCount(), "peer effects: access")
}

func TestAuthenticationAndAuthorizationInvalidationPreserveWireFixtures(t *testing.T) {
	t.Parallel()

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const hash = "345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"
	invalidator := &recordingInvalidator{}
	service := mustNewService(t, invalidator, &recordingDiagnostics{})
	sink := &securityRecordingSink{}
	fanout := &recordingFanout{}
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))

	service.AuthenticationCacheInvalidated(context.Background(), userID, []string{hash})
	service.InvalidateAuthorization(context.Background(), userID)
	service.InvalidateAuthorization(context.Background(), "")

	want := []peerBroadcast{
		{event: clusterEventSessionRevoked, data: []byte(`{"user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","session_ids":null,"access_token_hashes":["345bf502d577508c91103476c743217fa304f9a15e92c6bdc5810f891677891f"],"close_connections":false}`)},
		{event: clusterEventAuthorizationInvalidated, data: []byte(`{"user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb"}`)},
		{event: clusterEventAuthorizationInvalidated, data: []byte(`{}`)},
	}
	got := fanout.broadcasts
	require.Equal(t, want, got, "security wire fixtures\n got")
	require.Equal(t, 1, invalidator.accessCalls(), "authentication local effects: access")
	require.Equal(t, 1, invalidator.activityCalls(), "authentication local effects: access")
	require.Equal(t, 1, sink.userCloseCount(), "authorization local closes: user")
	require.Equal(t, 1, sink.allCloseCount(), "authorization local closes: user")

	for _, broadcast := range append([]peerBroadcast(nil), fanout.broadcasts...) {
		handler := fanout.handler(broadcast.event)
		require.NotNil(t, handler, "handler %q was not registered", broadcast.event)
		require.NoError(t, handler(context.Background(), broadcast.data), "peer handler %q", broadcast.event)
	}
	require.Len(t, fanout.broadcasts, 3, "peer security handlers rebroadcast")
}

func TestSecurityPropagationRejectsInvalidPeerAndLocalInputWithoutLeaks(t *testing.T) {
	t.Parallel()

	diagnostics := &recordingDiagnostics{}
	invalidator := &recordingInvalidator{}
	sink := &securityRecordingSink{}
	fanout := &recordingFanout{}
	service := mustNewService(t, invalidator, diagnostics)
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))

	const sensitive = "raw-session-or-user-value"
	service.SessionsRevoked(context.Background(), sensitive, []string{sensitive}, []string{sensitive})
	service.AuthenticationCacheInvalidated(context.Background(), sensitive, []string{sensitive})
	service.InvalidateAuthorization(context.Background(), sensitive)
	require.Empty(t, fanout.broadcasts, "invalid local input produced effects: broadcasts")
	require.Equal(t, 0, invalidator.accessCalls(), "invalid local input produced effects: broadcasts")
	require.Equal(t, 0, sink.totalCloses(), "invalid local input produced effects: broadcasts")
	require.Equal(t, 3, diagnostics.count(), "diagnostics count/text")
	require.NotContains(t, diagnostics.joined(), sensitive, "diagnostics count/text")

	require.Error(t, fanout.handler(clusterEventSessionRevoked)(context.Background(), []byte(`{"user_id":"invalid","session_ids":[],"access_token_hashes":[],"close_connections":true}`)), "invalid peer session revocation was accepted")
	require.Error(t, fanout.handler(clusterEventAuthorizationInvalidated)(context.Background(), []byte(`{"user_id":"invalid"}`)), "invalid peer authorization invalidation was accepted")
	require.Empty(t, fanout.broadcasts, "invalid peer input produced effects")
	require.Equal(t, 0, invalidator.accessCalls(), "invalid peer input produced effects")
	require.Equal(t, 0, sink.totalCloses(), "invalid peer input produced effects")
}

func TestSecurityPropagationMissingAndFailingFanoutIsDiagnosticOnly(t *testing.T) {
	t.Parallel()

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const sessionID = "cccccccccccccccccccccccccc"
	invalidator := &recordingInvalidator{}
	sink := &securityRecordingSink{}
	diagnostics := &recordingDiagnostics{}
	service := mustNewService(t, invalidator, diagnostics)
	require.NoError(t, service.SetSink(sink))
	service.SessionsRevoked(context.Background(), userID, []string{sessionID}, nil)
	service.AuthenticationCacheInvalidated(context.Background(), userID, nil)
	require.Equal(t, 2, invalidator.accessCalls(), "missing fanout effects: access")
	require.Equal(t, 1, sink.sessionCloseCount(), "missing fanout effects: access")
	require.Equal(t, 2, diagnostics.count(), "missing fanout effects: access")
	missingDiagnostics := diagnostics.joined()
	require.Contains(t, missingDiagnostics, securityOperationSessionRevocation)
	require.Contains(t, missingDiagnostics, securityOperationAuthenticationCache)
	require.Contains(t, missingDiagnostics, "realtime cluster fan-out is not attached")

	failureDiagnostics := &recordingDiagnostics{}
	serviceWithFailure := mustNewService(t, &recordingInvalidator{}, failureDiagnostics)
	secretFailure := "transport accidentally included credential-secret"
	fanout := &recordingFanout{broadcast: func(string, []byte) error { return errors.New(secretFailure) }}
	require.NoError(t, serviceWithFailure.SetClusterFanout(fanout))
	serviceWithFailure.InvalidateAuthorization(context.Background(), userID)
	failureText := failureDiagnostics.joined()
	require.Equal(t, 1, failureDiagnostics.count())
	require.Contains(t, failureText, securityOperationAuthorization)
	require.Contains(t, failureText, "realtime cluster fan-out failed")
	require.NotContains(t, failureText, secretFailure)
}

func TestSecurityPropagationWithoutSinkStillInvalidatesAndFansOut(t *testing.T) {
	t.Parallel()

	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	const sessionID = "cccccccccccccccccccccccccc"
	invalidator := &recordingInvalidator{}
	fanout := &recordingFanout{}
	service := mustNewService(t, invalidator, &recordingDiagnostics{})
	require.NoError(t, service.SetClusterFanout(fanout))

	service.SessionsRevoked(context.Background(), userID, []string{sessionID}, nil)
	service.InvalidateAuthorization(context.Background(), userID)
	require.Equal(t, 1, invalidator.accessCalls(), "authentication effects: access")
	require.Equal(t, 1, invalidator.activityCalls(), "authentication effects: access")
	require.Len(t, fanout.broadcasts, 2, "broadcasts")
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
	require.Error(t, err)
	require.Contains(t, err.Error(), "register authorization.invalidated cluster handler")
	wantRegistered := []string{clusterEventPublication, clusterEventExamAttemptUnbound,
		clusterEventSessionRevoked, clusterEventAuthorizationInvalidated}
	require.Equal(t, wantRegistered, registered, "registration order")
	err = service.SetClusterFanout(&recordingFanout{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already attached")
}

func TestConcurrentSecurityPropagation(t *testing.T) {
	t.Parallel()

	invalidator := &recordingInvalidator{}
	service := mustNewService(t, invalidator, &recordingDiagnostics{})
	sink := &securityRecordingSink{}
	fanout := &recordingFanout{}
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))

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
	require.Equal(t, 64, invalidator.accessCalls(), "concurrent local effects: access")
	require.Equal(t, 64, sink.sessionCloseCount(), "concurrent local effects: access")
	require.Len(t, fanout.broadcasts, 64, "concurrent broadcasts")
}

func TestSecurityCollaboratorsAreRequiredAtConstruction(t *testing.T) {
	t.Parallel()

	_, err := New(Dependencies{AuthenticationInvalidator: nil, Diagnostics: &recordingDiagnostics{}})
	require.Error(t, err, "nil invalidator was accepted")
	_, err = New(Dependencies{AuthenticationInvalidator: &recordingInvalidator{}, Diagnostics: nil})
	require.Error(t, err, "nil diagnostics were accepted")
}

func mustNewService(
	t *testing.T,
	invalidator AuthenticationInvalidator,
	diagnostics Diagnostics,
) *Service {
	t.Helper()
	service, err := New(Dependencies{AuthenticationInvalidator: invalidator, Diagnostics: diagnostics})
	require.NoError(t, err)
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
