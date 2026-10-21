// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamResourceRequiresBoundedAuthoredMetadataAndExactFileIdentity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	resource, err := model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "  Reference sheet  ", "Use **section 2**.", 0, at)
	require.NoError(t, err)
	require.Equal(t, "Reference sheet", resource.DisplayName)
	require.Equal(t, 0, resource.Position)
	require.False(t, resource.IsArchived())
	require.NoError(t, resource.Validate())

	for name, description := range map[string]string{
		"empty name":        "",
		"invalid name UTF8": string([]byte{0xff}),
		"long name":         strings.Repeat("é", model.ExamResourceDisplayNameMaxRunes+1),
	} {
		_, err = model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), description, "", 0, at)
		assert.Error(t, err, "%s accepted", name)
	}
	_, err = model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "Notes", strings.Repeat("a", model.ExamResourceDescriptionMaxBytes+1), 0, at)
	require.Error(t, err, "oversized Markdown description accepted")
	_, err = model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "Notes", string([]byte{0xff}), 0, at)
	require.Error(t, err, "invalid Markdown UTF-8 accepted")
	require.Equal(t, 15, utf8.RuneCountInString(resource.DisplayName), "display name rune count")
}

func TestExamResourceChangesDraftProjectionWithoutMutatingPublishedIdentity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	resource, err := model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "Reference", "", 0, at)
	require.NoError(t, err)
	originalRevision := resource.SelectedFileRevisionID
	replacement := model.NewFileRevisionID()
	changed, err := resource.ReplaceContent(replacement, at.Add(time.Minute))
	require.NoError(t, err, "ReplaceContent() changed=%v resource=%#v error", changed, resource)
	require.True(t, changed, "ReplaceContent() changed=%v resource=%#v error", changed, resource)
	require.Equal(t, replacement, resource.SelectedFileRevisionID, "ReplaceContent() changed=%v resource=%#v error", changed, resource)
	require.NotEqual(t, replacement, originalRevision, "ReplaceContent() changed=%v resource=%#v error", changed, resource)
	changed, err = resource.ApplyMetadata("Reference", "", at.Add(2*time.Minute))
	require.NoError(t, err, "no-op ApplyMetadata() changed")
	require.False(t, changed, "no-op ApplyMetadata() changed")
	changed, err = resource.ApplyMetadata(" Updated ", "New *note*", at.Add(2*time.Minute))
	require.NoError(t, err, "ApplyMetadata() changed=%v resource=%#v error", changed, resource)
	require.True(t, changed, "ApplyMetadata() changed=%v resource=%#v error", changed, resource)
	require.Equal(t, "Updated", resource.DisplayName, "ApplyMetadata() changed=%v resource=%#v error", changed, resource)
	err = resource.Archive(at.Add(3 * time.Minute))
	require.NoError(t, err, "Archive() resource=%#v error", resource)
	require.True(t, resource.IsArchived(), "Archive() resource=%#v error", resource)
}
