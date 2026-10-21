// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package job

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

type handlerFunc func(context.Context, Execution) Outcome

func (f handlerFunc) Run(ctx context.Context, execution Execution) Outcome {
	return f(ctx, execution)
}

func TestRegistryIsClosedImmutableAndVersionAware(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return Outcome{Kind: OutcomeSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}
	}))
	registry, err := NewRegistry([]Descriptor{descriptor})
	require.NoError(t, err)
	resolved, err := registry.Resolve(model.JobTypeProfilePictureGenerateDefault, 1)
	require.NoError(t, err, "Resolve() = %#v", resolved)
	require.Equal(t, descriptor.Type, resolved.Type, "Resolve() = %#v", resolved)
	descriptor.CommandVersions[0] = 9
	_, err = registry.Resolve(model.JobTypeProfilePictureGenerateDefault, 1)
	require.NoError(t, err)
	_, err = registry.Resolve(model.JobTypeProfilePictureGenerateDefault, 2)
	require.Error(t, err, "Resolve() accepted unsupported payload version")
}

func TestRegistryRejectsDuplicateTypesAndMissingHandlers(t *testing.T) {
	t.Parallel()
	valid := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	_, err := NewRegistry([]Descriptor{valid, valid})
	require.Error(t, err, "NewRegistry() accepted duplicate type")
	valid.Handler = nil
	_, err = NewRegistry([]Descriptor{valid})
	require.Error(t, err, "NewRegistry() accepted missing handler")
}
