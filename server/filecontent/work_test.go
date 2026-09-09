// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

const workTestSeed = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type contentWorkCase struct {
	name string
	body []byte
	run  func(*Content, context.Context, io.Reader) error
}

func contentWorkCases(t *testing.T) []contentWorkCase {
	t.Helper()
	var input bytes.Buffer
	if err := png.Encode(&input, image.NewNRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	return []contentWorkCase{
		{name: "resource", body: []byte("notes"), run: func(content *Content, ctx context.Context, body io.Reader) error {
			_, err := content.StoreExamResourceRendition(ctx, model.NewFileRevisionID(), model.NewFileRenditionID(), model.ExamResourceMediaText, body, 5, time.Unix(1, 0))
			return err
		}},
		{name: "resource delegation", body: []byte("notes"), run: func(content *Content, ctx context.Context, body io.Reader) error {
			_, err := content.StoreExamResource(ctx, model.NewFileRevisionID(), model.ExamResourceMediaText, body, 5, time.Unix(1, 0))
			return err
		}},
		{name: "profile normalization", body: input.Bytes(), run: func(content *Content, ctx context.Context, body io.Reader) error {
			_, err := content.NormalizeAndStoreProfilePicture(ctx, model.NewFileRevisionID(), body, int64(input.Len()), time.Unix(1, 0))
			return err
		}},
		{name: "default generation", run: func(content *Content, ctx context.Context, _ io.Reader) error {
			_, err := content.GenerateAndStoreDefaultProfilePicture(ctx, model.NewFileRevisionID(), workTestSeed, time.Unix(1, 0))
			return err
		}},
		{name: "fallback rendering", run: func(content *Content, ctx context.Context, _ io.Reader) error {
			picture, err := content.RenderDefaultProfilePicture(ctx, workTestSeed, 128)
			if err == nil {
				err = picture.Body.Close()
			}
			return err
		}},
	}
}

func TestContentWorkRequiresPositiveCapacity(t *testing.T) {
	t.Parallel()
	for _, maximum := range []int{-1, 0} {
		if _, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: maximum}, nil); err == nil {
			t.Fatalf("maximum=%d was accepted", maximum)
		}
	}
}

func TestContentWorkPipelinesShareCapacityAndRefuseBeforeInputOrStorage(t *testing.T) {
	cases := contentWorkCases(t)
	for _, holder := range cases {
		t.Run(holder.name, func(t *testing.T) {
			// Rendering speed under race and coverage instrumentation must not
			// determine whether the synchronization deadlines expire.
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				recorder := &contentWorkRecorder{onStart: func() { close(entered); <-release }}
				filesystem := &contentWorkVFS{FileSystem: memoryvfs.New()}
				content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, recorder)
				if err != nil {
					t.Fatal(err)
				}
				done, outcome := make(chan struct{}), make(chan error, 1)
				go func() {
					defer close(done)
					outcome <- holder.run(content, context.Background(), bytes.NewReader(holder.body))
				}()
				t.Cleanup(func() {
					releaseOnce.Do(func() { close(release) })
					awaitContentWork(t, done)
				})
				awaitContentWork(t, entered)
				for _, refused := range cases {
					body := &unreadContentWorkBody{}
					err := refused.run(content, context.Background(), body)
					var capacity interface{ WorkCapacityExceeded() }
					if !errors.Is(err, ErrWorkCapacity) || !errors.As(err, &capacity) {
						t.Fatalf("%s while %s runs: error=%v", refused.name, holder.name, err)
					}
					if body.reads.Load() != 0 || filesystem.writes.Load() != 0 {
						t.Fatalf("refused %s performed work: reads=%d writes=%d", refused.name, body.reads.Load(), filesystem.writes.Load())
					}
				}
				if recorder.started.Load() != 1 || recorder.active.Load() != 1 || recorder.rejected.Load() != int64(len(cases)) {
					t.Fatalf("admission metrics: started=%d active=%d rejected=%d", recorder.started.Load(), recorder.active.Load(), recorder.rejected.Load())
				}
				// Measure a known duration while the admitted operation is blocked.
				time.Sleep(time.Second)
				releaseOnce.Do(func() { close(release) })
				awaitContentWork(t, done)
				if err := <-outcome; err != nil {
					t.Fatalf("admitted %s: %v", holder.name, err)
				}
				if recorder.active.Load() != 0 || recorder.finished.Load() != 1 || recorder.duration.Load() != int64(time.Second) {
					t.Fatalf("completion metrics: active=%d finished=%d duration=%d", recorder.active.Load(), recorder.finished.Load(), recorder.duration.Load())
				}
			})
		})
	}
}

func TestContentWorkCancelledBeforeAdmissionDoesNoWork(t *testing.T) {
	for _, test := range contentWorkCases(t) {
		t.Run(test.name, func(t *testing.T) {
			recorder := &contentWorkRecorder{}
			filesystem := &contentWorkVFS{FileSystem: memoryvfs.New()}
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, recorder)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			body := &unreadContentWorkBody{}
			if err := test.run(content, ctx, body); !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want cancellation", err)
			}
			if body.reads.Load() != 0 || filesystem.writes.Load() != 0 || recorder.started.Load() != 0 || recorder.rejected.Load() != 0 {
				t.Fatal("cancelled request consumed processing capacity, input, or storage")
			}
			if err := test.run(content, context.Background(), bytes.NewReader(test.body)); err != nil {
				t.Fatalf("available capacity after cancellation: %v", err)
			}
		})
	}
}

func TestContentWorkCancellationDoesNotReleaseRunningReader(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	body := &blockedContentWorkBody{entered: entered, release: release}
	recorder := &contentWorkRecorder{}
	filesystem := &contentWorkVFS{FileSystem: memoryvfs.New()}
	content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, outcome := make(chan struct{}), make(chan error, 1)
	go func() {
		defer close(done)
		_, err := content.StoreExamResource(ctx, model.NewFileRevisionID(), model.ExamResourceMediaText, body, 5, time.Unix(1, 0))
		outcome <- err
	}()
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); awaitContentWork(t, done) })
	awaitContentWork(t, entered)
	cancel()
	if _, err := content.RenderDefaultProfilePicture(context.Background(), workTestSeed, 128); !errors.Is(err, ErrWorkCapacity) {
		t.Fatalf("capacity released while synchronous reader was still running: %v", err)
	}
	select {
	case <-done:
		t.Fatal("cancelled operation returned before its synchronous reader")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	awaitContentWork(t, done)
	if err := <-outcome; !errors.Is(err, context.Canceled) || filesystem.writes.Load() != 0 || recorder.active.Load() != 0 {
		t.Fatalf("cancelled outcome=%v writes=%d active=%d", err, filesystem.writes.Load(), recorder.active.Load())
	}
	if _, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader("notes"), 5, time.Unix(1, 0)); err != nil {
		t.Fatalf("slot was not released after synchronous work returned: %v", err)
	}
}

func TestContentWorkCancellationAfterAdmissionStopsBeforeProcessing(t *testing.T) {
	for _, test := range contentWorkCases(t) {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			recorder := &contentWorkRecorder{onStart: cancel}
			filesystem := &contentWorkVFS{FileSystem: memoryvfs.New()}
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, recorder)
			if err != nil {
				t.Fatal(err)
			}
			body := &unreadContentWorkBody{}
			if err := test.run(content, ctx, body); !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want cancellation", err)
			}
			if body.reads.Load() != 0 || filesystem.writes.Load() != 0 || recorder.started.Load() != 1 || recorder.finished.Load() != 1 || recorder.active.Load() != 0 {
				t.Fatal("cancelled admitted operation processed content or retained capacity")
			}
		})
	}
}

func TestContentWorkFailureReleasesCapacityAndDoesNotGateExactReadsOrRemoval(t *testing.T) {
	content, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, storeErr := content.StoreExamResource(t.Context(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader("\x00"), 1, time.Unix(1, 0)); !errors.Is(storeErr, ErrInvalidExamResourceContent) {
		t.Fatalf("invalid content error=%v", storeErr)
	}
	revision := model.NewFileRevisionID()
	rendition, err := content.StoreExamResource(context.Background(), revision, model.ExamResourceMediaText, strings.NewReader("notes"), 5, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("capacity after validation failure: %v", err)
	}
	finish, err := content.beginWork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	opened, err := content.OpenExamResource(context.Background(), revision, rendition.ID)
	if err != nil {
		t.Fatalf("exact read while processing is full: %v", err)
	}
	got, err := io.ReadAll(opened)
	_ = opened.Close()
	if err != nil || string(got) != "notes" {
		t.Fatalf("exact content=%q error=%v", got, err)
	}
	if err := content.RemoveExamResource(context.Background(), revision, rendition.ID); err != nil {
		t.Fatalf("removal while processing is full: %v", err)
	}
	if err := content.PurgeAbandonedFileRevision(context.Background(), revision); err != nil {
		t.Fatalf("purge while processing is full: %v", err)
	}
	if _, _, err := content.StageOnboardingImport(context.Background(), model.NewOnboardingImportID(), strings.NewReader("name\nstudent\n"), 64); err != nil {
		t.Fatalf("streaming onboarding import while processing is full: %v", err)
	}
	if _, err := content.StageStarterWorkspaceObject(context.Background(), model.NewStarterWorkspaceObjectID(), strings.NewReader("notes"), 5, "text/plain"); err != nil {
		t.Fatalf("streaming Workspace file while processing is full: %v", err)
	}
}

func TestContentWorkReadSeekerBoundsReadsAndStopsBeforeCancelledIO(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := strings.NewReader(strings.Repeat("a", 2*examResourceCopyBuffer))
	reader := workReadSeeker{ctx: ctx, reader: source}
	body := make([]byte, 2*examResourceCopyBuffer)
	if n, err := reader.Read(body); err != nil || n != examResourceCopyBuffer {
		t.Fatalf("bounded read=%d error=%v", n, err)
	}
	if offset, err := reader.Seek(7, io.SeekStart); err != nil || offset != 7 {
		t.Fatalf("live seek=%d error=%v", offset, err)
	}
	cancel()
	if n, err := reader.Read(body); !errors.Is(err, context.Canceled) || n != 0 {
		t.Fatalf("cancelled read=%d error=%v", n, err)
	}
	if _, err := reader.Seek(0, io.SeekStart); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled seek error=%v", err)
	}
	if offset, err := source.Seek(0, io.SeekCurrent); err != nil || offset != 7 {
		t.Fatalf("cancelled I/O moved source: offset=%d error=%v", offset, err)
	}
}

type contentWorkRecorder struct {
	started  atomic.Int64
	active   atomic.Int64
	finished atomic.Int64
	rejected atomic.Int64
	duration atomic.Int64
	onStart  func()
}

func (r *contentWorkRecorder) Started() {
	r.active.Add(1)
	if r.started.Add(1) == 1 && r.onStart != nil {
		r.onStart()
	}
}
func (r *contentWorkRecorder) Finished(duration time.Duration) {
	r.duration.Add(int64(duration))
	r.finished.Add(1)
	r.active.Add(-1)
}
func (r *contentWorkRecorder) Rejected() { r.rejected.Add(1) }

type contentWorkVFS struct {
	vfspkg.FileSystem
	writes atomic.Int64
}

func (f *contentWorkVFS) Write(ctx context.Context, path string, body io.Reader, options vfspkg.WriteOptions) (vfspkg.Info, error) {
	f.writes.Add(1)
	return f.FileSystem.Write(ctx, path, body, options)
}

type unreadContentWorkBody struct{ reads atomic.Int64 }

func (b *unreadContentWorkBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	return 0, errors.New("input must not be read")
}

type blockedContentWorkBody struct {
	entered chan struct{}
	release <-chan struct{}
}

func (b *blockedContentWorkBody) Read(body []byte) (int, error) {
	close(b.entered)
	<-b.release
	return copy(body, "notes"), io.EOF
}

func awaitContentWork(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("content operation did not reach its expected boundary")
	}
}
