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
	"reflect"
	"strings"
	"testing"
	"time"

	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

func TestCorrectionApplyPreservesOptionalInputsAndOwnsManifest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID(), CredentialScopes: []string{"exam:manage"}},
		model.RequestMetadata{RequestID: "correction-apply", IPAddress: "192.0.2.1", UserAgent: "test"})
	for _, test := range []struct {
		name         string
		instructions ExamSittingCorrectionInstructions
		policy       ExamSittingCorrectionBrowserPolicy
		resources    []ExamSittingCorrectionResourceManifestItem
	}{
		{name: "omitted inputs and nil manifest"},
		{name: "present empty inputs and empty manifest", instructions: ExamSittingCorrectionInstructions{Present: true},
			policy:    ExamSittingCorrectionBrowserPolicy{Present: true, Policy: model.DisabledBrowserPolicy()},
			resources: []ExamSittingCorrectionResourceManifestItem{}},
		{name: "authored inputs and ordered manifest", instructions: ExamSittingCorrectionInstructions{Present: true, Markdown: " **Read** "},
			policy: ExamSittingCorrectionBrowserPolicy{Present: true, Policy: model.BrowserPolicy{SchemaVersion: 1, Enabled: true,
				StartRuleID: "notes", Rules: []model.BrowserPolicyRule{{RuleID: "notes", Origin: "https://notes.example", PathPrefix: "/"}}}},
			resources: []ExamSittingCorrectionResourceManifestItem{
				{ResourceID: model.NewExamResourceID(), DisplayName: " Second ", DescriptionMarkdown: " **Second** ", StageID: model.NewExamCorrectionResourceStageID()},
				{ResourceID: model.NewExamResourceID(), DisplayName: " First "},
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := ApplyExamSittingCorrectionCommand{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
				ExpectedSittingRevision: 2, ExpectedCurrentRevisionID: model.NewExamRevisionID(), Instructions: test.instructions,
				BrowserPolicy: test.policy, Resources: test.resources, AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityTerminal, model.CandidateCapabilityWorkspace}, CandidateSummary: " raw summary ", AcknowledgementRequired: true,
				PrivateReason: " raw reason ", IdempotencyKey: " raw-key "}
			fake := &examCorrectionUseCasesFake{}
			if _, err := (&App{examCorrections: fake}).ApplyExamSittingCorrection(ctx, invocation, command); err != nil {
				t.Fatal(err)
			}
			assertCorrectionFacadeInvocation(t, fake, ctx, invocation)
			if fake.apply.Instructions != command.Instructions ||
				!reflect.DeepEqual(fake.apply.BrowserPolicy, command.BrowserPolicy) ||
				fake.apply.IdempotencyKey != command.IdempotencyKey || fake.apply.PrivateReason != command.PrivateReason ||
				fake.apply.CandidateSummary != command.CandidateSummary || !fake.apply.AcknowledgementRequired {
				t.Fatalf("optional presence or raw input changed: %#v", fake.apply)
			}
			if fake.apply.Resources == nil || len(fake.apply.Resources) != len(command.Resources) {
				t.Fatalf("manifest must retain length and normalize nil to empty: %#v", fake.apply.Resources)
			}
			for index, item := range command.Resources {
				if fake.apply.Resources[index] != item {
					t.Fatalf("manifest order or content changed: %#v", fake.apply.Resources)
				}
				fake.apply.Resources[index].DisplayName = "changed by child"
				if command.Resources[index] != item {
					t.Fatal("child manifest shares the caller's backing array")
				}
			}
		})
	}
}

func TestCorrectionStageForwardsBodyDigestAndRawKey(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID(), CredentialScopes: []string{"exam:manage"}},
		model.RequestMetadata{RequestID: "correction-stage", IPAddress: "192.0.2.1", UserAgent: "test"})
	body := strings.NewReader("one")
	command := StageExamSittingCorrectionResourceContentCommand{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		BaseRevisionID: model.NewExamRevisionID(), Target: ExamSittingCorrectionResourceReplacement, ResourceID: model.NewExamResourceID(),
		MediaType: model.ExamResourceMediaText, Body: body, Size: 3, ExpectedSHA256: strings.Repeat("A", 64), IdempotencyKey: " raw-key "}
	result := examcorrection.ResourceStage{StageID: model.NewExamCorrectionResourceStageID(), ResourceID: command.ResourceID,
		MediaType: command.MediaType, Size: command.Size, SHA256: command.ExpectedSHA256, ExpiresAt: time.Now()}
	fake := &examCorrectionUseCasesFake{stageResult: result}
	got, err := (&App{examCorrections: fake}).StageExamSittingCorrectionResourceContent(ctx, invocation, command)
	if err != nil || got != ExamSittingCorrectionResourceStage(result) {
		t.Fatalf("stage result = %#v, %v", got, err)
	}
	if fake.stage != command || body.Len() != 3 {
		t.Fatalf("stage input changed or upload consumed: %#v, unread bytes = %d", fake.stage, body.Len())
	}
	assertCorrectionFacadeInvocation(t, fake, ctx, invocation)
}

func TestCorrectionApplyNarrowsInternalResult(t *testing.T) {
	t.Parallel()
	want := ExamSittingCorrectionResult{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		PreviousRevisionID: model.NewExamRevisionID(), RevisionID: model.NewExamRevisionID(), RevisionNumber: 5,
		SittingState: model.ExamSittingPaused, SittingRevision: 7, EffectiveAt: time.Now()}
	fake := &examCorrectionUseCasesFake{applyResult: examcorrection.Result{
		ExamID: want.ExamID, SittingID: want.SittingID, PreviousRevisionID: want.PreviousRevisionID, RevisionID: want.RevisionID,
		RevisionNumber: want.RevisionNumber, SittingState: want.SittingState, SittingRevision: want.SittingRevision,
		EffectiveAt: want.EffectiveAt, AcknowledgementRequired: true, Replayed: true,
	}}
	got, err := (&App{examCorrections: fake}).ApplyExamSittingCorrection(context.Background(), Invocation{}, ApplyExamSittingCorrectionCommand{})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("correction result = %#v, %v; want %#v", got, err, want)
	}
	for _, private := range []string{"AcknowledgementRequired", "Replayed"} {
		if _, exists := reflect.TypeOf(got).FieldByName(private); exists {
			t.Fatalf("public result exposes internal %s", private)
		}
	}
}

func TestCorrectionFacadeConcealsFailuresAndDiscardsResults(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		cause  error
		code   string
		fields map[string]string
	}{
		{"denied", NewError("authorization.denied"), "resource.not_found", nil},
		{"missing", &examcorrection.Fault{Code: "exam.sitting.correction.not_found"}, "resource.not_found", nil},
		{"safe fields", &examcorrection.Fault{Code: "exam.sitting.correction.invalid", SafeFields: map[string]any{"field": "resources"}}, "exam.sitting.correction.invalid", map[string]string{"field": "resources"}},
		{"busy", &examcorrection.Fault{Code: "service.busy"}, "service.busy", nil},
		{"dependency", errors.New("storage unavailable"), "exam.sitting.correction.unavailable", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &examCorrectionUseCasesFake{err: test.cause,
				stageResult: examcorrection.ResourceStage{StageID: model.NewExamCorrectionResourceStageID()},
				applyResult: examcorrection.Result{ExamID: model.NewExamID(), Replayed: true}}
			application := &App{examCorrections: fake}
			stage, stageErr := application.StageExamSittingCorrectionResourceContent(context.Background(), Invocation{}, StageExamSittingCorrectionResourceContentCommand{})
			result, applyErr := application.ApplyExamSittingCorrection(context.Background(), Invocation{}, ApplyExamSittingCorrectionCommand{})
			if stage != (ExamSittingCorrectionResourceStage{}) || !reflect.DeepEqual(result, ExamSittingCorrectionResult{}) {
				t.Fatalf("failed command returned a result: %#v, %#v", stage, result)
			}
			for _, err := range []error{stageErr, applyErr} {
				mapped, ok := As(err)
				if !ok || mapped.Code() != test.code || !reflect.DeepEqual(mapped.Fields(), test.fields) || !errors.Is(err, test.cause) {
					t.Fatalf("error = %#v; want %s, fields %v, retained cause", mapped, test.code, test.fields)
				}
			}
		})
	}
}

func assertCorrectionFacadeInvocation(t *testing.T, fake *examCorrectionUseCasesFake, ctx context.Context, invocation Invocation) {
	t.Helper()
	if fake.ctx != ctx || !reflect.DeepEqual(fake.call.Principal(), invocation.Principal()) || fake.call.RequestMetadata() != invocation.RequestMetadata() {
		t.Fatalf("child call lost context, principal, or request metadata: %#v", fake.call)
	}
	principal := fake.call.Principal()
	principal.CredentialScopes[0] = "changed"
	if !reflect.DeepEqual(fake.call.Principal(), invocation.Principal()) || invocation.Principal().CredentialScopes[0] != "exam:manage" {
		t.Fatal("child call exposed mutable principal scopes")
	}
}

func TestCorrectionEffectPublishesManagerAndCandidateRefetchFacts(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	candidateID := model.NewUserID()
	collections := examCollectionInvalidationEffects{
		sittings: &examCollectionInvalidationStoreFake{candidateIDs: []model.UserID{candidateID}},
		realtime: realtime,
	}
	previousRevisionID, revisionID := model.NewExamRevisionID(), model.NewExamRevisionID()
	at := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	err := (examCorrectionRealtimeEffects{realtime: realtime, collections: collections}).Corrected(context.Background(), examcorrection.Result{
		ExamID: examID, SittingID: sittingID, PreviousRevisionID: previousRevisionID,
		RevisionID: revisionID, SittingRevision: 7, EffectiveAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 4 || events[0].Action != model.ActionExamSittingView ||
		events[1].Action != model.ActionExamSittingParticipate || events[0].Name != "exam_sitting_content_corrected" ||
		events[1].Name != events[0].Name || string(events[1].Data) != string(events[0].Data) ||
		events[2].Name != "manager.sitting_board.changed" ||
		events[3].Name != "candidate.exam_activity.changed" || events[3].UserID != candidateID.String() {
		t.Fatalf("events = %#v", events)
	}
	for _, test := range []struct {
		selected    []model.CandidateCapability
		required    bool
		wantRelease int
	}{
		{[]model.CandidateCapability{model.CandidateCapabilityBrowser}, true, 0},
		{[]model.CandidateCapability{model.CandidateCapabilityTerminal}, false, 0},
		{[]model.CandidateCapability{model.CandidateCapabilityTerminal}, true, 1},
	} {
		execution := &correctionExecutionFake{}
		err := (examCorrectionRealtimeEffects{realtime: realtime, collections: collections, execution: execution}).Corrected(context.Background(), examcorrection.Result{
			ExamID: examID, SittingID: sittingID, PreviousRevisionID: previousRevisionID,
			RevisionID: revisionID, SittingRevision: 7, EffectiveAt: at,
			AffectedCapabilities: test.selected, AcknowledgementRequired: test.required,
		})
		if err != nil || execution.releases != test.wantRelease {
			t.Fatalf("correction release=%d, want=%d, err=%v", execution.releases, test.wantRelease, err)
		}
	}

}

type examCorrectionUseCasesFake struct {
	ctx         context.Context
	call        examcorrection.Call
	stage       examcorrection.StageResourceContentCommand
	apply       examcorrection.ApplyCommand
	stageResult examcorrection.ResourceStage
	applyResult examcorrection.Result
	err         error
}

func (f *examCorrectionUseCasesFake) StageResourceContent(ctx context.Context, call examcorrection.Call, c examcorrection.StageResourceContentCommand) (examcorrection.ResourceStage, error) {
	f.ctx, f.call, f.stage = ctx, call, c
	return f.stageResult, f.err
}
func (f *examCorrectionUseCasesFake) Apply(ctx context.Context, call examcorrection.Call, c examcorrection.ApplyCommand) (examcorrection.Result, error) {
	f.ctx, f.call, f.apply = ctx, call, c
	return f.applyResult, f.err
}

// This verifies selection of the existing protective release, not actual freeze.
type correctionExecutionFake struct{ releases int }

func (fake *correctionExecutionFake) ReleaseSitting(context.Context, model.ExamSittingID) error {
	fake.releases++
	return nil
}
