// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRealtimeEventValidateForPublish(t *testing.T) {
	t.Parallel()

	valid := RealtimeEvent{
		Name:   "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   model.NewID(),
		},
	}
	require.NoError(t, valid.ValidateForPublish())
	require.NoError(t, (RealtimeEvent{Name: "user.notification", UserID: model.NewID()}).ValidateForPublish())

	tests := []struct {
		name  string
		event RealtimeEvent
	}{
		{name: "empty name", event: RealtimeEvent{UserID: model.NewID()}},
		{name: "missing target", event: RealtimeEvent{Name: "orphan.event"}},
		{name: "invalid data", event: RealtimeEvent{
			Name: "x", UserID: model.NewID(), Data: json.RawMessage(`{`),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, test.event.ValidateForPublish(), "expected validation error")
		})
	}
}

func TestExamAuthoringEventsHaveTypedSafePayloads(t *testing.T) {
	t.Parallel()
	examID := model.NewExamID()
	created, err := NewExamCreatedEvent(examID)
	require.NoError(t, err)
	require.Equal(t, "exam_created", created.Name)
	require.Equal(t, model.ActionExamView, created.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExam, ID: examID.String()}, created.Resource)
	require.Empty(t, created.Data)
	updated, err := NewExamDraftUpdatedEvent(examID, 7)
	require.NoError(t, err)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","draft_revision":7}`, string(updated.Data))
	_, err = NewExamDraftUpdatedEvent(examID, 0)
	require.Error(t, err, "accepted non-positive Draft revision")
	sittingID := model.NewExamSittingID()
	sittingChangedAt := time.Date(2026, 8, 14, 9, 5, 0, 123456789, time.UTC)
	sittingScheduled, err := NewExamSittingScheduledEvent(examID, sittingID, model.ExamSittingScheduled, 1, sittingChangedAt)
	require.NoError(t, err)
	require.Equal(t, "exam_sitting_scheduled", sittingScheduled.Name)
	require.Equal(t, model.ActionExamSittingView, sittingScheduled.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, sittingScheduled.Resource)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`","state":"scheduled","revision":1,"changed_at":"2026-08-14T09:05:00.123456Z"}`, string(sittingScheduled.Data))
	sittingCanceled, err := NewExamSittingCanceledEvent(examID, sittingID, model.ExamSittingCanceled, 2, sittingChangedAt)
	require.NoError(t, err)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`","state":"canceled","revision":2,"changed_at":"2026-08-14T09:05:00.123456Z"}`, string(sittingCanceled.Data))
	_, err = NewExamSittingCanceledEvent(examID, sittingID, model.ExamSittingScheduled, 2, sittingChangedAt)
	require.Error(t, err, "accepted a cancellation event with non-canceled state")
	lifecycle, err := NewExamSittingLifecycleChangedEvent(examID, sittingID, model.ExamSittingPaused, 4,
		"manager_paused", sittingChangedAt.Add(2*time.Hour), sittingChangedAt)
	require.NoError(t, err)
	require.Equal(t, "exam_sitting_lifecycle_changed", lifecycle.Name)
	require.Equal(t, model.ActionExamSittingView, lifecycle.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, lifecycle.Resource)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`","state":"paused","revision":4,"reason_code":"manager_paused","scheduled_end_at":"2026-08-14T11:05:00.123456Z","changed_at":"2026-08-14T09:05:00.123456Z"}`, string(lifecycle.Data))
	_, err = NewExamSittingLifecycleChangedEvent(examID, sittingID, model.ExamSittingPaused, 4,
		"private teacher reason", sittingChangedAt.Add(2*time.Hour), sittingChangedAt)
	require.Error(t, err, "accepted private lifecycle event reason")
	opened, err := NewExamSittingLifecycleChangedEvent(examID, sittingID, model.ExamSittingOpen, 2,
		"scheduled_start_reached", sittingChangedAt.Add(2*time.Hour), sittingChangedAt)
	require.NoError(t, err)
	require.Contains(t, string(opened.Data), `"reason_code":"scheduled_start_reached"`)
	sealed, err := NewExamSittingLifecycleChangedEvent(examID, sittingID, model.ExamSittingClosed, 6,
		"sealing_completed", sittingChangedAt.Add(2*time.Hour), sittingChangedAt)
	require.NoError(t, err)
	got := string(sealed.Data)
	require.Contains(t, got, `"state":"closed"`)
	require.Contains(t, got, `"reason_code":"sealing_completed"`)
	previousRevisionID, correctedRevisionID := model.NewExamRevisionID(), model.NewExamRevisionID()
	correctedAt := time.Date(2026, 8, 14, 9, 5, 0, 123456789, time.UTC)
	corrected, err := NewExamSittingContentCorrectedEvent(examID, sittingID, previousRevisionID, correctedRevisionID, 7, correctedAt)
	require.NoError(t, err)
	require.Equal(t, "exam_sitting_content_corrected", corrected.Name)
	require.Equal(t, model.ActionExamSittingView, corrected.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, corrected.Resource)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","exam_sitting_id":"`+sittingID.String()+`","previous_revision_id":"`+previousRevisionID.String()+`","revision_id":"`+correctedRevisionID.String()+`","sitting_revision":7,"effective_at":"2026-08-14T09:05:00.123456Z"}`, string(corrected.Data))
	_, err = NewExamSittingContentCorrectedEvent(examID, sittingID, previousRevisionID, previousRevisionID, 7, correctedAt)
	require.Error(t, err, "accepted a correction whose Revision did not change")
	revisionID := model.NewExamRevisionID()
	policyDigest := strings.Repeat("a", 64)
	publishedAt := time.Date(2026, 8, 14, 9, 10, 0, 123456789, time.UTC)
	published, err := NewExamRevisionPublishedEvent(revisionID, examID, 3, policyDigest, model.ExamRevisionPublicationStandard, publishedAt)
	require.NoError(t, err)
	require.Equal(t, "exam_revision_published", published.Name)
	require.Equal(t, model.ActionExamView, published.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExam, ID: examID.String()}, published.Resource)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","revision_id":"`+revisionID.String()+`","number":3,"policy_digest":"`+policyDigest+`","kind":"standard","published_at":"2026-08-14T09:10:00.123456Z"}`, string(published.Data))
	workspaceEntryID := model.NewStarterWorkspaceEntryID()
	workspaceChangedAt := time.Date(2026, 8, 14, 9, 15, 0, 123456789, time.UTC)
	workspaceChanged, err := NewExamStarterWorkspaceChangedEvent(examID, workspaceEntryID, 8, "file_replaced", workspaceChangedAt)
	require.NoError(t, err)
	require.Equal(t, "exam_starter_workspace_changed", workspaceChanged.Name)
	require.Equal(t, model.ActionExamView, workspaceChanged.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExam, ID: examID.String()}, workspaceChanged.Resource)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","entry_id":"`+workspaceEntryID.String()+`","operation":"file_replaced","draft_revision":8,"changed_at":"2026-08-14T09:15:00.123456Z"}`, string(workspaceChanged.Data))
	_, err = NewExamStarterWorkspaceChangedEvent(examID, workspaceEntryID, 8, "path_and_checksum_changed", workspaceChangedAt)
	require.Error(t, err, "accepted unbounded Starter Workspace operation")
	archivedAt := time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC)
	archived, err := NewExamArchivedEvent(examID, 8, archivedAt)
	require.NoError(t, err)
	require.Equal(t, "exam_archived", archived.Name)
	require.Equal(t, model.ActionExamView, archived.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExam, ID: examID.String()}, archived.Resource)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","exam_revision":8,"archived_at":"2026-08-14T09:30:00Z"}`, string(archived.Data))
	_, err = NewExamArchivedEvent(examID, 0, archivedAt)
	require.Error(t, err, "accepted non-positive Exam revision")
	managerID := model.NewUserID()
	managerChanged, err := NewExamManagerChangedEvent(examID, managerID, true, 9, archivedAt)
	require.NoError(t, err, "Manager event = %#v", managerChanged)
	require.Equal(t, "exam_manager_changed", managerChanged.Name, "Manager event = %#v", managerChanged)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","user_id":"`+managerID.String()+`","present":true,"exam_revision":9,"changed_at":"2026-08-14T09:30:00Z"}`, string(managerChanged.Data), "Manager event = %#v", managerChanged)
	ownerChanged, err := NewExamOwnerTransferredEvent(examID, managerID, 10, archivedAt)
	require.NoError(t, err, "owner event = %#v", ownerChanged)
	require.Equal(t, "exam_owner_transferred", ownerChanged.Name, "owner event = %#v", ownerChanged)
	require.Equal(t, `{"exam_id":"`+examID.String()+`","owner_user_id":"`+managerID.String()+`","exam_revision":10,"changed_at":"2026-08-14T09:30:00Z"}`, string(ownerChanged.Data), "owner event = %#v", ownerChanged)
}

func TestPublishClonesGeneratesIDAndDeliversLocalFirst(t *testing.T) {
	t.Parallel()

	order := &orderedCalls{}
	sink := &recordingSink{publish: func(event RealtimeEvent) {
		order.add("local")
		if len(event.Data) != 0 {
			event.Data[0] = '['
		}
	}}
	fanout := &recordingFanout{broadcast: func(_ string, _ []byte) error {
		order.add("peer")
		return nil
	}}
	service := newOrdinaryTestService(t)
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))

	data := json.RawMessage(`{"ok":true}`)
	event := RealtimeEvent{Name: "user.notification", UserID: model.NewID(), Data: data}
	require.NoError(t, service.Publish(context.Background(), event))
	require.Empty(t, event.ID)
	require.Equal(t, `{"ok":true}`, string(data), "caller data was mutated")
	// Contract: ordinary publication delivers locally before the best-effort
	// peer fan-out.
	require.Equal(t, []string{"local", "peer"}, order.snapshot())
	require.Len(t, sink.events, 1)
	require.NotEmpty(t, sink.events[0].ID)
	require.Len(t, fanout.broadcasts, 1)
	require.Equal(t, clusterEventPublication, fanout.broadcasts[0].event)
	require.NotContains(t, string(fanout.broadcasts[0].data), `"data":[`, "sink mutation reached peer payload")
}

func TestPublishPreservesOrdinaryPeerWireFixtureAndDoesNotRebroadcast(t *testing.T) {
	t.Parallel()

	const eventID = "yyyyyyyyyyyyyyyyyyyyyyyyyy"
	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	service := newOrdinaryTestService(t)
	sink := &recordingSink{}
	fanout := &recordingFanout{}
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))

	event := RealtimeEvent{
		ID:     eventID,
		Name:   "user.notification",
		UserID: userID,
		Data:   json.RawMessage(`{"ok":true}`),
	}
	require.NoError(t, service.Publish(context.Background(), event))
	want := `{"event":{"id":"yyyyyyyyyyyyyyyyyyyyyyyyyy","event":"user.notification","user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","resource":{"type":"","id":""},"data":{"ok":true}}}`
	got := string(fanout.broadcasts[0].data)
	require.Equal(t, want, got, "ordinary peer wire payload\n got")

	handler := fanout.handler(clusterEventPublication)
	require.NotNil(t, handler, "ordinary publication handler was not registered")
	require.NoError(t, handler(context.Background(), fanout.broadcasts[0].data))
	require.Len(t, sink.events, 2, "local events after peer delivery")
	require.Len(t, fanout.broadcasts, 1, "peer delivery rebroadcast")
}

func TestUnbindExamAttemptConnectionAppliesExactLocalEffectBeforePeerFanout(t *testing.T) {
	t.Parallel()

	connectionID := model.NewAttemptConnectionID()
	order := &orderedCalls{}
	sink := &recordingSink{unbind: func(got model.AttemptConnectionID) {
		require.Equal(t, connectionID, got, "unbound Connection")
		order.add("local")
	}}
	fanout := &recordingFanout{broadcast: func(_ string, _ []byte) error {
		order.add("peer")
		return nil
	}}
	service := newOrdinaryTestService(t)
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))

	require.NoError(t, service.UnbindExamAttemptConnection(context.Background(), connectionID))
	// Contract: ordinary publication delivers locally before the best-effort
	// peer fan-out.
	require.Equal(t, []string{"local", "peer"}, order.snapshot())
	require.Len(t, fanout.broadcasts, 1)
	require.Equal(t, clusterEventExamAttemptUnbound, fanout.broadcasts[0].event)
	require.Equal(t, `{"attempt_connection_id":"`+connectionID.String()+`"}`, string(fanout.broadcasts[0].data))

	handler := fanout.handler(clusterEventExamAttemptUnbound)
	require.NotNil(t, handler, "Attempt unbind handler was not registered")
	require.NoError(t, handler(context.Background(), fanout.broadcasts[0].data))
	require.Equal(t, 2, sink.unboundCount(), "peer effect count")
	require.Len(t, fanout.broadcasts, 1, "peer effect count")
}

func TestPeerExamAttemptUnbindRejectsNonCanonicalPayloadWithoutLocalEffect(t *testing.T) {
	t.Parallel()

	service := newOrdinaryTestService(t)
	sink := &recordingSink{}
	fanout := &recordingFanout{}
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(fanout))
	handler := fanout.handler(clusterEventExamAttemptUnbound)
	connectionID := model.NewAttemptConnectionID()
	payload := []byte(`{"attempt_connection_id":"` + connectionID.String() + `","credential":"forbidden"}`)
	require.Error(t, handler(context.Background(), payload), "peer unbind accepted an unknown payload field")
	require.Equal(t, 0, sink.unboundCount(), "invalid peer unbound")
}

func TestPublishMissingCollaboratorsAndTypedFailures(t *testing.T) {
	t.Parallel()

	t.Run("missing sink is a no-op", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		fanout := &recordingFanout{}
		require.NoError(t, service.SetClusterFanout(fanout))
		require.NoError(t, service.Publish(context.Background(), validUserEvent()))
		require.Len(t, fanout.broadcasts, 1, "broadcasts")
	})

	t.Run("missing fanout fails after local delivery", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		sink := &recordingSink{}
		require.NoError(t, service.SetSink(sink))
		err := service.Publish(context.Background(), validUserEvent())
		var delivery *DeliveryError
		require.ErrorAs(t, err, &delivery)
		require.Len(t, sink.events, 1, "local events")
	})

	t.Run("fanout failure is typed", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		fanout := &recordingFanout{broadcast: func(string, []byte) error {
			return errors.New("peer unavailable")
		}}
		require.NoError(t, service.SetClusterFanout(fanout))
		err := service.Publish(context.Background(), validUserEvent())
		var delivery *DeliveryError
		require.ErrorAs(t, err, &delivery)
	})

	t.Run("invalid publication has no effects", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		sink := &recordingSink{}
		fanout := &recordingFanout{}
		require.NoError(t, service.SetSink(sink))
		require.NoError(t, service.SetClusterFanout(fanout))
		err := service.Publish(context.Background(), RealtimeEvent{Name: "missing.target"})
		var invalid *InvalidPublicationError
		require.ErrorAs(t, err, &invalid)
		require.Empty(t, sink.events, "invalid publication effects: local")
		require.Empty(t, fanout.broadcasts, "invalid publication effects: local")
	})
}

func TestCollaboratorAttachmentIsThreadSafeAndOnce(t *testing.T) {
	t.Parallel()

	service := newOrdinaryTestService(t)
	var sinkSuccesses atomic.Int64
	var fanoutSuccesses atomic.Int64
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(2)
		go func() {
			defer group.Done()
			if service.SetSink(&recordingSink{}) == nil {
				sinkSuccesses.Add(1)
			}
		}()
		go func() {
			defer group.Done()
			if service.SetClusterFanout(&recordingFanout{}) == nil {
				fanoutSuccesses.Add(1)
			}
		}()
	}
	group.Wait()
	require.Equal(t, int64(1), sinkSuccesses.Load(), "attachment successes: sink")
	require.Equal(t, int64(1), fanoutSuccesses.Load(), "attachment successes: sink")
	require.Error(t, service.SetSink(nil), "nil sink was accepted")
	require.Error(t, service.SetClusterFanout(nil), "nil fanout was accepted")
}

func TestClusterAttachmentRegistrationFailureIsTerminal(t *testing.T) {
	t.Parallel()

	service := newOrdinaryTestService(t)
	fanout := &recordingFanout{register: func(string) error {
		return errors.New("registration failed")
	}}
	err := service.SetClusterFanout(fanout)
	require.Error(t, err)
	require.Contains(t, err.Error(), "register websocket.publish cluster handler")
	err = service.SetClusterFanout(&recordingFanout{})
	require.Error(t, err, "retry error")
	require.Contains(t, err.Error(), "already attached", "retry error")
	require.NoError(t, service.Publish(context.Background(), validUserEvent()))
}

func TestServiceHasNoBackgroundLifecycle(t *testing.T) {
	t.Parallel()

	typeOfService := reflect.TypeOf(newOrdinaryTestService(t))
	for _, method := range []string{"Start", "Stop", "Close", "Run"} {
		_, exists := typeOfService.MethodByName(method)
		require.False(t, exists, "Service unexpectedly exposes background lifecycle method %s", method)
	}
}

func validUserEvent() RealtimeEvent {
	return RealtimeEvent{Name: "user.notification", UserID: model.NewID()}
}

func newOrdinaryTestService(t *testing.T) *Service {
	t.Helper()
	service, err := New(Dependencies{AuthenticationInvalidator: &recordingInvalidator{}, Diagnostics: &recordingDiagnostics{}})
	require.NoError(t, err)
	return service
}

type orderedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (o *orderedCalls) add(call string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, call)
}

func (o *orderedCalls) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.calls...)
}

type recordingSink struct {
	mu      sync.Mutex
	events  []RealtimeEvent
	publish func(RealtimeEvent)
	unbound []model.AttemptConnectionID
	unbind  func(model.AttemptConnectionID)
}

func (s *recordingSink) UnbindExamAttemptConnection(value model.AttemptConnectionID) {
	s.mu.Lock()
	s.unbound = append(s.unbound, value)
	unbind := s.unbind
	s.mu.Unlock()
	if unbind != nil {
		unbind(value)
	}
}

func (s *recordingSink) unboundCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.unbound)
}

func (s *recordingSink) PublishLocal(_ context.Context, event RealtimeEvent) {
	s.mu.Lock()
	s.events = append(s.events, event.Clone())
	publish := s.publish
	s.mu.Unlock()
	if publish != nil {
		publish(event)
	}
}

func (*recordingSink) CloseSession(string, ConnectionCloseReason) {}
func (*recordingSink) CloseUser(string, ConnectionCloseReason)    {}
func (*recordingSink) CloseAll(ConnectionCloseReason)             {}

type peerBroadcast struct {
	event string
	data  []byte
}

type recordingFanout struct {
	mu         sync.Mutex
	handlers   map[string]func(context.Context, []byte) error
	broadcasts []peerBroadcast
	register   func(string) error
	broadcast  func(string, []byte) error
}

func (f *recordingFanout) RegisterHandler(
	event string,
	handler func(context.Context, []byte) error,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.register != nil {
		if err := f.register(event); err != nil {
			return err
		}
	}
	if f.handlers == nil {
		f.handlers = make(map[string]func(context.Context, []byte) error)
	}
	if _, exists := f.handlers[event]; exists {
		return fmt.Errorf("handler %s already registered", event)
	}
	f.handlers[event] = handler
	return nil
}

func (f *recordingFanout) Broadcast(_ context.Context, event string, data []byte) error {
	f.mu.Lock()
	f.broadcasts = append(f.broadcasts, peerBroadcast{event: event, data: append([]byte(nil), data...)})
	broadcast := f.broadcast
	f.mu.Unlock()
	if broadcast != nil {
		return broadcast(event, data)
	}
	return nil
}

func (f *recordingFanout) handler(event string) func(context.Context, []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handlers[event]
}
