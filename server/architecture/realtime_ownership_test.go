// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRealtimeContractsHaveOneProductionOwner(t *testing.T) {
	t.Parallel()

	parentContracts := map[string]bool{
		"RealtimeEvent":         true,
		"ConnectionCloseReason": true,
		"RealtimeSink":          true,
		"RealtimeClusterFanout": true,
	}
	parentMechanics := map[string]bool{
		"realtimePublication":                 true,
		"realtimeEventMessage":                true,
		"sessionRevocationMessage":            true,
		"authorizationInvalidationMessage":    true,
		"handlePeerPublication":               true,
		"handlePeerSessionRevocation":         true,
		"handlePeerAuthorizationInvalidation": true,
		"publishLocal":                        true,
		"broadcastSecurityInvalidation":       true,
		"applySessionRevocation":              true,
		"applyAuthorizationInvalidation":      true,
	}

	files, err := filepath.Glob(filepath.Join("..", "app", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, declaration := range parsed.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if ok && (parentContracts[typeSpec.Name.Name] || parentMechanics[typeSpec.Name.Name]) {
						t.Errorf("parent app redeclares Realtime child ownership in %s: %s", filepath.Base(path), typeSpec.Name.Name)
					}
				}
			case *ast.FuncDecl:
				if parentMechanics[declaration.Name.Name] {
					t.Errorf("parent app redeclares Realtime child mechanic in %s: %s", filepath.Base(path), declaration.Name.Name)
				}
			}
		}
	}
}

func TestRealtimePeerContractsAreDeclaredOnlyByChild(t *testing.T) {
	t.Parallel()

	peerContracts := map[string]bool{
		"websocket.publish":              true,
		"authentication.session_revoked": true,
		"authorization.invalidated":      true,
	}
	serverRoot := filepath.Clean("..")
	err := filepath.WalkDir(serverRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != serverRoot && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && peerContracts[value] {
				relative, relErr := filepath.Rel(serverRoot, path)
				if relErr != nil || !strings.HasPrefix(filepath.ToSlash(relative), "app/realtime/") {
					t.Errorf("peer contract %q is owned outside app/realtime in %s", value, path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRealtimeAdaptationProvenanceFollowsOwnedSource(t *testing.T) {
	t.Parallel()

	packageDoc, err := os.ReadFile(filepath.Join("..", "app", "realtime", "doc.go"))
	if err != nil {
		t.Fatal(err)
	}
	notice, err := os.ReadFile(filepath.Join("..", "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"app/realtime/doc.go": string(packageDoc), "NOTICE": string(notice)} {
		if !strings.Contains(content, "10b780cb097b2ec94ab0f9df7ebcbd5b7850f13f") {
			t.Errorf("%s does not preserve the exact Mattermost revision", name)
		}
	}
	if !strings.Contains(string(notice), "server/app/realtime/") {
		t.Error("NOTICE does not point to the canonical Realtime child package")
	}
}
