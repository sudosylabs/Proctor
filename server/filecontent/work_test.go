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

	"github.com/stretchr/testify/require"

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
	require.NoError(t, png.Encode(&input, image.NewNRGBA(image.Rect(0, 0, 16, 16))))
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
		_, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: maximum}, nil)
		require.Error(t, err, "maximum=%d was accepted", maximum)
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
				require.NoError(t, err)
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
					require.ErrorIs(t, err, ErrWorkCapacity, "%s while %s runs: error", refused.name, holder.name)
					require.ErrorAs(t, err, &capacity, "%s while %s runs: error", refused.name, holder.name)
					require.Equal(t, int64(0), body.reads.Load(), "refused")
					require.Equal(t, int64(0), filesystem.writes.Load(), "refused")
				}
				require.Equal(t, int64(1), recorder.started.Load(), "admission metrics: started")
				require.Equal(t, int64(1), recorder.active.Load(), "admission metrics: started")
				require.Equal(t, int64(len(cases)), recorder.rejected.Load(), "admission metrics: started")
				// Measure a known duration while the admitted operation is blocked.
				time.Sleep(time.Second)
				releaseOnce.Do(func() { close(release) })
				awaitContentWork(t, done)
				require.NoError(t, (<-outcome), "admitted %s", holder.name)
				require.Equal(t, int64(0), recorder.active.Load(), "completion metrics: active")
				require.Equal(t, int64(1), recorder.finished.Load(), "completion metrics: active")
				require.Equal(t, int64(time.Second), recorder.duration.Load(), "completion metrics: active")
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
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			body := &unreadContentWorkBody{}
			require.ErrorIs(t, test.run(content, ctx, body), context.Canceled)
			require.Equal(t, int64(0), body.reads.Load(), "cancelled request consumed processing capacity, input, or storage")
			require.Equal(t, int64(0), filesystem.writes.Load(), "cancelled request consumed processing capacity, input, or storage")
			require.Equal(t, int64(0), recorder.started.Load(), "cancelled request consumed processing capacity, input, or storage")
			require.Equal(t, int64(0), recorder.rejected.Load(), "cancelled request consumed processing capacity, input, or storage")
			require.NoError(t, test.run(content, context.Background(), bytes.NewReader(test.body)))
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
	require.NoError(t, err)
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
	_, err = content.RenderDefaultProfilePicture(context.Background(), workTestSeed, 128)
	require.ErrorIs(t, err, ErrWorkCapacity)
	select {
	case <-done:
		require.FailNow(t, "cancelled operation returned before its synchronous reader")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	awaitContentWork(t, done)
	require.ErrorIs(t, (<-outcome), context.Canceled, "cancelled outcome")
	require.Equal(t, int64(0), filesystem.writes.Load(), "cancelled outcome")
	require.Equal(t, int64(0), recorder.active.Load(), "cancelled outcome")
	_, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader("notes"), 5, time.Unix(1, 0))
	require.NoError(t, err)
}

func TestContentWorkCancellationAfterAdmissionStopsBeforeProcessing(t *testing.T) {
	for _, test := range contentWorkCases(t) {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			recorder := &contentWorkRecorder{onStart: cancel}
			filesystem := &contentWorkVFS{FileSystem: memoryvfs.New()}
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, recorder)
			require.NoError(t, err)
			body := &unreadContentWorkBody{}
			require.ErrorIs(t, test.run(content, ctx, body), context.Canceled)
			require.Equal(t, int64(0), body.reads.Load(), "cancelled admitted operation processed content or retained capacity")
			require.Equal(t, int64(0), filesystem.writes.Load(), "cancelled admitted operation processed content or retained capacity")
			require.Equal(t, int64(1), recorder.started.Load(), "cancelled admitted operation processed content or retained capacity")
			require.Equal(t, int64(1), recorder.finished.Load(), "cancelled admitted operation processed content or retained capacity")
			require.Equal(t, int64(0), recorder.active.Load(), "cancelled admitted operation processed content or retained capacity")
		})
	}
}

func TestContentWorkFailureReleasesCapacityAndDoesNotGateExactReadsOrRemoval(t *testing.T) {
	content, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 1}, nil)
	require.NoError(t, err)
	_, storeErr := content.StoreExamResource(t.Context(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader("\x00"), 1, time.Unix(1, 0))
	require.ErrorIs(t, storeErr, ErrInvalidExamResourceContent)
	revision := model.NewFileRevisionID()
	rendition, err := content.StoreExamResource(context.Background(), revision, model.ExamResourceMediaText, strings.NewReader("notes"), 5, time.Unix(1, 0))
	require.NoError(t, err)
	finish, err := content.beginWork(context.Background())
	require.NoError(t, err)
	defer finish()
	opened, err := content.OpenExamResource(context.Background(), revision, rendition.ID)
	require.NoError(t, err)
	got, err := io.ReadAll(opened)
	_ = opened.Close()
	require.NoError(t, err, "exact content")
	require.Equal(t, "notes", string(got), "exact content")
	require.NoError(t, content.RemoveExamResource(context.Background(), revision, rendition.ID))
	require.NoError(t, content.PurgeAbandonedFileRevision(context.Background(), revision))
	_, _, err = content.StageOnboardingImport(context.Background(), model.NewOnboardingImportID(), strings.NewReader("name\nstudent\n"), 64)
	require.NoError(t, err)
	_, err = content.StageStarterWorkspaceObject(context.Background(), model.NewStarterWorkspaceObjectID(), strings.NewReader("notes"), 5, "text/plain")
	require.NoError(t, err)
}

func TestContentWorkReadSeekerBoundsReadsAndStopsBeforeCancelledIO(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := strings.NewReader(strings.Repeat("a", 2*examResourceCopyBuffer))
	reader := workReadSeeker{ctx: ctx, reader: source}
	body := make([]byte, 2*examResourceCopyBuffer)
	n, err := reader.Read(body)
	require.NoError(t, err, "bounded read")
	require.Equal(t, examResourceCopyBuffer, n, "bounded read")
	offset, err := reader.Seek(7, io.SeekStart)
	require.NoError(t, err, "live seek")
	require.Equal(t, int64(7), offset, "live seek")
	cancel()
	n, err = reader.Read(body)
	require.ErrorIs(t, err, context.Canceled, "cancelled read")
	require.Equal(t, 0, n, "cancelled read")
	_, err = reader.Seek(0, io.SeekStart)
	require.ErrorIs(t, err, context.Canceled)
	offset, err = source.Seek(0, io.SeekCurrent)
	require.NoError(t, err, "cancelled I/O moved source: offset")
	require.Equal(t, int64(7), offset, "cancelled I/O moved source: offset")
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
		require.FailNow(t, "content operation did not reach its expected boundary")
	}
}
