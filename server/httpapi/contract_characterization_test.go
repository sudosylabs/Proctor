// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

// TestV1HTTPContractCharacterization makes any change to the reviewed v1
// operations, their referenced request/response components, or the compiled
// runtime manifest an explicit compatibility decision. The checked-in OpenAPI
// document is the public source of truth for success statuses, headers, DTOs,
// and public errors; the runtime projection proves the production catalog
// still exposes the corresponding method, path, authentication, and migrated
// error metadata.
func TestV1HTTPContractCharacterization(t *testing.T) {
	t.Parallel()

	documentBytes := readOpenAPIDocumentBytes(t)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(documentBytes, &document))
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, productionResources(agreementResourceApplications(), nil, BuildInfo{}, browserCookies{}, nil)...))
	runtimeRoutes, err := json.Marshal(runtimeAPI.Routes())
	require.NoError(t, err)
	projection := map[string]json.RawMessage{
		"openapi":        document["openapi"],
		"paths":          document["paths"],
		"components":     document["components"],
		"runtime_routes": runtimeRoutes,
	}
	canonical, err := json.Marshal(projection)
	require.NoError(t, err)
	digest := sha256.Sum256(canonical)
	gotDigest := hex.EncodeToString(digest[:])
	const wantDigest = "a3ea207c5d51b72f00271730fb48865021afccbdc35cf7f80092ee888d202414"
	require.Equal(t, wantDigest, gotDigest, "v1 HTTP contract digest")

	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(documentBytes, &parsed))
	operationCount := 0
	for _, item := range parsed.Paths {
		for method := range item {
			if isHTTPMethod(strings.ToUpper(method)) {
				operationCount++
			}
		}
	}
	require.Equal(t, 279, operationCount, "v1 operation count")
	require.Len(t, runtimeAPI.Routes(), operationCount, "runtime route count")
}

func readOpenAPIDocumentBytes(t *testing.T) []byte {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "locate contract characterization test")
	path := filepath.Join(filepath.Dir(currentFile), "..", "openapi.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
