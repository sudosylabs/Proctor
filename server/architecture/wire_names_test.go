// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"fmt"
	"go/ast"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// wireTransportPackages own the HTTP and realtime wire shapes. Every struct
// with a json tag declared in them is a transport DTO.
var wireTransportPackages = map[string]bool{
	serverModule + "/httpapi":      true,
	serverModule + "/websocket":    true,
	serverModule + "/app/realtime": true,
}

// wireSnakeCase is the member grammar: lowercase snake_case words, so an
// identifier initialism is spelled id, never Id or ID.
var wireSnakeCase = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// retiredWireTimestampNames are the millisecond-era member names.
var retiredWireTimestampNames = map[string]string{
	"create_at": "created_at", "update_at": "updated_at", "delete_at": "archived_at",
}

// scheduledWireInstants are the instant members that keep a noun form because
// they name a schedule, a deadline, a validity bound, an observation, or a
// named state rather than a completed lifecycle event (those are
// <verb>ed_at). Adding a word here is a wire-dialect decision. A member is
// accepted when its name ends in one of these words.
var scheduledWireInstants = []string{
	"start_at",     // scheduled or effective start bound
	"end_at",       // scheduled or effective end bound
	"expires_at",   // validity bound
	"retry_at",     // earliest retry
	"active_at",    // membership effective-instant selector
	"available_at", // earliest availability
	"effective_at", // when a decision takes effect
	"eligible_at",  // retention eligibility
	"stale_at",     // when a record becomes stale
	"closing_at",   // scheduled closure
	"heartbeat_at", // last observed heartbeat
	"activity_at",  // last observed activity
	"login_at",     // last observed login
	"ready_at",     // entry into the Ready state, named for the state
}

type wireMember struct {
	pkg     *packages.Package
	owner   string
	field   *ast.Field
	name    string
	options string
	tagged  bool
}

// TestTransportWireMembersFollowTheNamingDialect keeps every HTTP and realtime
// DTO member in one dialect: explicit snake_case json names, no retired
// millisecond-era names, and instants named <verb>ed_at for a lifecycle event
// or by a scheduled noun form.
func TestTransportWireMembersFollowTheNamingDialect(t *testing.T) {
	t.Parallel()
	violations := wireMemberViolations(typedServerPackages(t), wireTransportPackages)
	for _, violation := range violations {
		assert.Fail(t, violation)
	}
}

func TestTransportWireMemberRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"httpapi/dto.go": `package httpapi

import "time"

type Optional[T any] struct{ value T }

type examResponse struct {
	ID          string            ` + "`json:\"examId\"`" + `
	UserID      string            ` + "`json:\"user_Id\"`" + `
	CreateAt    int64             ` + "`json:\"create_at\"`" + `
	UpdatedAt   int64             ` + "`json:\"updated_at\"`" + `
	ArchivedAt  *int64            ` + "`json:\"archived_at,omitempty\"`" + `
	ExpiresAt   Optional[int64]   ` + "`json:\"expires_at\"`" + `
	PublishAt   string            ` + "`json:\"publish_at\"`" + `
	Untagged    string
	Unnamed     string            ` + "`json:\",omitempty\"`" + `
	ReleasedAt  time.Time         ` + "`json:\"released_at\"`" + `
	StartAt     Optional[string]  ` + "`json:\"start_at\"`" + `
	Secret      string            ` + "`json:\"-\"`" + `
	internal    string
}
`,
	})
	violations := wireMemberViolations(loaded, map[string]bool{"fixture/httpapi": true})
	expected := []string{
		`dto.go:8: fixture/httpapi.examResponse.ID json member "examId" must be snake_case`,
		`dto.go:9: fixture/httpapi.examResponse.UserID json member "user_Id" must be snake_case`,
		`dto.go:10: fixture/httpapi.examResponse.CreateAt json member "create_at" is retired; use created_at`,
		`dto.go:14: fixture/httpapi.examResponse.PublishAt json member "publish_at" names an instant; use a <verb>ed_at lifecycle name or a scheduled noun form`,
		`dto.go:15: fixture/httpapi.examResponse.Untagged has no json tag; name the wire member explicitly or tag it "-"`,
		`dto.go:16: fixture/httpapi.examResponse.Unnamed has no json member name; name the wire member explicitly`,
	}
	require.Equal(t, expected, violations)
}

func wireMemberViolations(loaded []*packages.Package, transports map[string]bool) []string {
	var violations []string
	for _, pkg := range loaded {
		if !transports[pkg.PkgPath] {
			continue
		}
		for _, file := range pkg.Syntax {
			owners := make(map[*ast.StructType]string)
			ast.Inspect(file, func(node ast.Node) bool {
				if spec, ok := node.(*ast.TypeSpec); ok {
					if structure, ok := spec.Type.(*ast.StructType); ok {
						owners[structure] = spec.Name.Name
					}
				}
				return true
			})
			ast.Inspect(file, func(node ast.Node) bool {
				structure, ok := node.(*ast.StructType)
				if !ok {
					return true
				}
				owner, declared := owners[structure]
				if !declared {
					owner = "<anonymous>"
				}
				members := structMembers(pkg, owner, structure)
				if !anyTagged(members) {
					return true
				}
				for _, member := range members {
					violations = append(violations, checkWireMember(member)...)
				}
				return true
			})
		}
	}
	return violations
}

func structMembers(pkg *packages.Package, owner string, structure *ast.StructType) []wireMember {
	var members []wireMember
	for _, field := range structure.Fields.List {
		member := wireMember{pkg: pkg, owner: owner, field: field}
		if field.Tag != nil {
			tag, err := strconv.Unquote(field.Tag.Value)
			if err == nil {
				if value, ok := reflect.StructTag(tag).Lookup("json"); ok {
					member.tagged = true
					member.name, member.options, _ = strings.Cut(value, ",")
				}
			}
		}
		members = append(members, member)
	}
	return members
}

func anyTagged(members []wireMember) bool {
	for _, member := range members {
		if member.tagged {
			return true
		}
	}
	return false
}

func checkWireMember(member wireMember) []string {
	field := member.field
	embedded := len(field.Names) == 0
	goName := ""
	if !embedded {
		goName = field.Names[0].Name
		if !ast.IsExported(goName) {
			return nil
		}
	}
	location := fmt.Sprintf("%s: %s.%s.%s", sourcePosition(member.pkg, field.Pos()), member.pkg.PkgPath, member.owner, goName)
	switch {
	case member.name == "-" && member.options == "":
		return nil
	case !member.tagged && embedded, member.name == "" && embedded:
		return nil // an embedded struct contributes its own members
	case !member.tagged:
		return []string{location + ` has no json tag; name the wire member explicitly or tag it "-"`}
	case member.name == "":
		return []string{location + " has no json member name; name the wire member explicitly"}
	}
	var violations []string
	if !wireSnakeCase.MatchString(member.name) {
		violations = append(violations, fmt.Sprintf("%s json member %q must be snake_case", location, member.name))
	}
	if replacement, retired := retiredWireTimestampNames[member.name]; retired {
		violations = append(violations, fmt.Sprintf("%s json member %q is retired; use %s", location, member.name, replacement))
	}
	if !isWireInstantName(member.name) {
		return violations
	}
	if _, retired := retiredWireTimestampNames[member.name]; !retired && strings.HasSuffix(member.name, "_at") && !conformingInstantName(member.name) {
		violations = append(violations, fmt.Sprintf("%s json member %q names an instant; use a <verb>ed_at lifecycle name or a scheduled noun form", location, member.name))
	}
	return violations
}

// isWireInstantName reports whether a member names an instant. It follows the
// OpenAPI agreement suite's timestamp rule (isWireTimestampName in httpapi).
func isWireInstantName(name string) bool {
	if _, retired := retiredWireTimestampNames[name]; retired {
		return true
	}
	if name == "awaiting_deadline" { // a count of records awaiting their deadline
		return false
	}
	return strings.HasSuffix(name, "_at") || strings.HasSuffix(name, "deadline") ||
		name == "message_date" || name == "server_time" || name == "database_time"
}

func conformingInstantName(name string) bool {
	if strings.HasSuffix(name, "ed_at") {
		return true
	}
	for _, scheduled := range scheduledWireInstants {
		if name == scheduled || strings.HasSuffix(name, "_"+scheduled) {
			return true
		}
	}
	return false
}
