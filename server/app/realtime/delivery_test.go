// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

	"github.com/sudosylabs/proctor/server/model"
)

func TestRealtimeEventValidateForPublish(t *testing.T) {
	t.Parallel()

	valid := RealtimeEvent{
		Name:   "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   model.NewId(),
		},
	}
	if err := valid.ValidateForPublish(); err != nil {
		t.Fatalf("valid event: %v", err)
	}
	if err := (RealtimeEvent{Name: "user.notification", UserID: model.NewId()}).ValidateForPublish(); err != nil {
		t.Fatalf("user-targeted event: %v", err)
	}

	tests := []struct {
		name  string
		event RealtimeEvent
	}{
		{name: "empty name", event: RealtimeEvent{UserID: model.NewId()}},
		{name: "missing target", event: RealtimeEvent{Name: "orphan.event"}},
		{name: "invalid data", event: RealtimeEvent{
			Name: "x", UserID: model.NewId(), Data: json.RawMessage(`{`),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.event.ValidateForPublish(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestExamAuthoringEventsHaveTypedSafePayloads(t *testing.T) {
	t.Parallel()
	examID := model.NewExamID()
	created, err := NewExamCreatedEvent(examID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "exam_created" || created.Action != model.ActionExamView || created.Resource != (model.Resource{Type: model.ResourceExam, ID: examID.String()}) || len(created.Data) != 0 {
		t.Fatalf("created event = %#v", created)
	}
	updated, err := NewExamDraftUpdatedEvent(examID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(updated.Data); got != `{"exam_id":"`+examID.String()+`","draft_revision":7}` {
		t.Fatalf("updated data = %s", got)
	}
	if _, err := NewExamDraftUpdatedEvent(examID, 0); err == nil {
		t.Fatal("accepted non-positive Draft revision")
	}
	workspaceEntryID := model.NewStarterWorkspaceEntryID()
	workspaceChangedAt := time.Date(2026, 8, 14, 9, 15, 0, 123, time.UTC)
	workspaceChanged, err := NewExamStarterWorkspaceChangedEvent(examID, workspaceEntryID, 8, "file_replaced", workspaceChangedAt)
	if err != nil {
		t.Fatal(err)
	}
	if workspaceChanged.Name != "exam_starter_workspace_changed" || workspaceChanged.Action != model.ActionExamView ||
		workspaceChanged.Resource != (model.Resource{Type: model.ResourceExam, ID: examID.String()}) {
		t.Fatalf("Starter Workspace event = %#v", workspaceChanged)
	}
	if got := string(workspaceChanged.Data); got != `{"exam_id":"`+examID.String()+`","entry_id":"`+workspaceEntryID.String()+`","operation":"file_replaced","draft_revision":8,"changed_at":"2026-08-14T09:15:00.000000123Z"}` {
		t.Fatalf("Starter Workspace data = %s", got)
	}
	if _, err = NewExamStarterWorkspaceChangedEvent(examID, workspaceEntryID, 8, "path_and_checksum_changed", workspaceChangedAt); err == nil {
		t.Fatal("accepted unbounded Starter Workspace operation")
	}
	archivedAt := time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC)
	archived, err := NewExamArchivedEvent(examID, 8, archivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Name != "exam_archived" || archived.Action != model.ActionExamView || archived.Resource != (model.Resource{Type: model.ResourceExam, ID: examID.String()}) {
		t.Fatalf("archived event = %#v", archived)
	}
	if got := string(archived.Data); got != `{"exam_id":"`+examID.String()+`","exam_revision":8,"archived_at":"2026-08-14T09:30:00Z"}` {
		t.Fatalf("archived data = %s", got)
	}
	if _, err := NewExamArchivedEvent(examID, 0, archivedAt); err == nil {
		t.Fatal("accepted non-positive Exam revision")
	}
	managerID := model.NewUserID()
	managerChanged, err := NewExamManagerChangedEvent(examID, managerID, true, 9, archivedAt)
	if err != nil || managerChanged.Name != "exam_manager_changed" || string(managerChanged.Data) != `{"exam_id":"`+examID.String()+`","user_id":"`+managerID.String()+`","present":true,"exam_revision":9,"changed_at":"2026-08-14T09:30:00Z"}` {
		t.Fatalf("Manager event = %#v, %v", managerChanged, err)
	}
	ownerChanged, err := NewExamOwnerTransferredEvent(examID, managerID, 10, archivedAt)
	if err != nil || ownerChanged.Name != "exam_owner_transferred" || string(ownerChanged.Data) != `{"exam_id":"`+examID.String()+`","owner_user_id":"`+managerID.String()+`","exam_revision":10,"changed_at":"2026-08-14T09:30:00Z"}` {
		t.Fatalf("owner event = %#v, %v", ownerChanged, err)
	}
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
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}

	data := json.RawMessage(`{"ok":true}`)
	event := RealtimeEvent{Name: "user.notification", UserID: model.NewId(), Data: data}
	if err := service.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if event.ID != "" {
		t.Fatalf("caller event ID was mutated: %q", event.ID)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("caller data was mutated: %s", data)
	}
	if got := order.snapshot(); !reflect.DeepEqual(got, []string{"local", "peer"}) {
		t.Fatalf("delivery order = %#v", got)
	}
	if len(sink.events) != 1 || sink.events[0].ID == "" {
		t.Fatalf("local events = %#v", sink.events)
	}
	if len(fanout.broadcasts) != 1 || fanout.broadcasts[0].event != clusterEventPublication {
		t.Fatalf("peer broadcasts = %#v", fanout.broadcasts)
	}
	if strings.Contains(string(fanout.broadcasts[0].data), `"data":[`) {
		t.Fatalf("sink mutation reached peer payload: %s", fanout.broadcasts[0].data)
	}
}

func TestPublishPreservesOrdinaryPeerWireFixtureAndDoesNotRebroadcast(t *testing.T) {
	t.Parallel()

	const eventID = "yyyyyyyyyyyyyyyyyyyyyyyyyy"
	const userID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	service := newOrdinaryTestService(t)
	sink := &recordingSink{}
	fanout := &recordingFanout{}
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(fanout); err != nil {
		t.Fatal(err)
	}

	event := RealtimeEvent{
		ID:     eventID,
		Name:   "user.notification",
		UserID: userID,
		Data:   json.RawMessage(`{"ok":true}`),
	}
	if err := service.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	want := `{"event":{"id":"yyyyyyyyyyyyyyyyyyyyyyyyyy","event":"user.notification","user_id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","resource":{"type":"","id":""},"data":{"ok":true}}}`
	if got := string(fanout.broadcasts[0].data); got != want {
		t.Fatalf("ordinary peer wire payload\n got: %s\nwant: %s", got, want)
	}

	handler := fanout.handler(clusterEventPublication)
	if handler == nil {
		t.Fatal("ordinary publication handler was not registered")
	}
	if err := handler(context.Background(), fanout.broadcasts[0].data); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("local events after peer delivery = %d, want 2", len(sink.events))
	}
	if len(fanout.broadcasts) != 1 {
		t.Fatalf("peer delivery rebroadcast %d events", len(fanout.broadcasts))
	}
}

func TestPublishMissingCollaboratorsAndTypedFailures(t *testing.T) {
	t.Parallel()

	t.Run("missing sink is a no-op", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		fanout := &recordingFanout{}
		if err := service.SetClusterFanout(fanout); err != nil {
			t.Fatal(err)
		}
		if err := service.Publish(context.Background(), validUserEvent()); err != nil {
			t.Fatal(err)
		}
		if len(fanout.broadcasts) != 1 {
			t.Fatalf("broadcasts = %d, want 1", len(fanout.broadcasts))
		}
	})

	t.Run("missing fanout fails after local delivery", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		sink := &recordingSink{}
		if err := service.SetSink(sink); err != nil {
			t.Fatal(err)
		}
		err := service.Publish(context.Background(), validUserEvent())
		var delivery *DeliveryError
		if !errors.As(err, &delivery) {
			t.Fatalf("error = %T %v, want DeliveryError", err, err)
		}
		if len(sink.events) != 1 {
			t.Fatalf("local events = %d, want 1", len(sink.events))
		}
	})

	t.Run("fanout failure is typed", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		fanout := &recordingFanout{broadcast: func(string, []byte) error {
			return errors.New("peer unavailable")
		}}
		if err := service.SetClusterFanout(fanout); err != nil {
			t.Fatal(err)
		}
		err := service.Publish(context.Background(), validUserEvent())
		var delivery *DeliveryError
		if !errors.As(err, &delivery) {
			t.Fatalf("error = %T %v, want DeliveryError", err, err)
		}
	})

	t.Run("invalid publication has no effects", func(t *testing.T) {
		service := newOrdinaryTestService(t)
		sink := &recordingSink{}
		fanout := &recordingFanout{}
		if err := service.SetSink(sink); err != nil {
			t.Fatal(err)
		}
		if err := service.SetClusterFanout(fanout); err != nil {
			t.Fatal(err)
		}
		err := service.Publish(context.Background(), RealtimeEvent{Name: "missing.target"})
		var invalid *InvalidPublicationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %T %v, want InvalidPublicationError", err, err)
		}
		if len(sink.events) != 0 || len(fanout.broadcasts) != 0 {
			t.Fatalf("invalid publication effects: local=%d peer=%d", len(sink.events), len(fanout.broadcasts))
		}
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
	if sinkSuccesses.Load() != 1 || fanoutSuccesses.Load() != 1 {
		t.Fatalf("attachment successes: sink=%d fanout=%d", sinkSuccesses.Load(), fanoutSuccesses.Load())
	}
	if err := service.SetSink(nil); err == nil {
		t.Fatal("nil sink was accepted")
	}
	if err := service.SetClusterFanout(nil); err == nil {
		t.Fatal("nil fanout was accepted")
	}
}

func TestClusterAttachmentRegistrationFailureIsTerminal(t *testing.T) {
	t.Parallel()

	service := newOrdinaryTestService(t)
	fanout := &recordingFanout{register: func(string) error {
		return errors.New("registration failed")
	}}
	if err := service.SetClusterFanout(fanout); err == nil ||
		!strings.Contains(err.Error(), "register websocket.publish cluster handler") {
		t.Fatalf("SetClusterFanout() error = %v", err)
	}
	if err := service.SetClusterFanout(&recordingFanout{}); err == nil ||
		!strings.Contains(err.Error(), "already attached") {
		t.Fatalf("retry error = %v, want terminal already-attached failure", err)
	}
	if err := service.Publish(context.Background(), validUserEvent()); err != nil {
		t.Fatalf("failed attachment no longer retained its fanout: %v", err)
	}
}

func TestServiceHasNoBackgroundLifecycle(t *testing.T) {
	t.Parallel()

	typeOfService := reflect.TypeOf(newOrdinaryTestService(t))
	for _, method := range []string{"Start", "Stop", "Close", "Run"} {
		if _, exists := typeOfService.MethodByName(method); exists {
			t.Fatalf("Service unexpectedly exposes background lifecycle method %s", method)
		}
	}
}

func validUserEvent() RealtimeEvent {
	return RealtimeEvent{Name: "user.notification", UserID: model.NewId()}
}

func newOrdinaryTestService(t *testing.T) *Service {
	t.Helper()
	service, err := New(&recordingInvalidator{}, &recordingDiagnostics{})
	if err != nil {
		t.Fatal(err)
	}
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
