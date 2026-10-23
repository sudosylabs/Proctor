// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

// TestJobTypesListEveryDeclaredJobType keeps the closed vocabulary that host
// inspection lists across in step with every JobType constant the package
// declares, wherever it is declared.
func TestJobTypesListEveryDeclaredJobType(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	declared := map[model.JobType]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		require.NoError(t, err)
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || value.Type == nil {
					continue
				}
				if ident, ok := value.Type.(*ast.Ident); !ok || ident.Name != "JobType" {
					continue
				}
				for _, literal := range value.Values {
					text, err := strconv.Unquote(literal.(*ast.BasicLit).Value)
					require.NoError(t, err)
					declared[model.JobType(text)] = true
				}
			}
		}
	}

	listed := map[model.JobType]bool{}
	for _, jobType := range model.JobTypes() {
		require.False(t, listed[jobType], "%s is listed twice", jobType)
		listed[jobType] = true
		require.True(t, jobType.IsValid(), "%s", jobType)
	}
	require.Equal(t, declared, listed)
	require.False(t, model.JobType("unknown.type").IsValid())
}

func TestJobStatusesAreTheClosedLifecycleVocabulary(t *testing.T) {
	t.Parallel()

	require.Equal(t, []model.JobStatus{
		model.JobStatusQueued, model.JobStatusRunning, model.JobStatusCancelRequested,
		model.JobStatusSucceeded, model.JobStatusFailed, model.JobStatusCanceled,
	}, model.JobStatuses())
	require.False(t, model.JobStatus("paused").IsValid())
}
