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
	"go/types"
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

// wireTimestampExemptions are DTO members that name an instant but are not a
// wire timestamp, keyed by "<package>.<type>.<member>".
var wireTimestampExemptions = map[string]string{
	// The membership page cursor is an opaque, versioned keyset envelope that
	// mirrors the application's millisecond query; the public active_at query
	// parameter and Link header carry RFC 3339.
	serverModule + "/httpapi.membershipPageCursor.active_at": "opaque cursor keyset",
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
// millisecond-era names, and instants carried as RFC 3339 strings under a
// <verb>ed_at lifecycle name or a scheduled noun form.
func TestTransportWireMembersFollowTheNamingDialect(t *testing.T) {
	t.Parallel()
	violations := wireMemberViolations(typedServerPackages(t), wireTransportPackages, wireTimestampExemptions)
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
	CursorAt    int64             ` + "`json:\"cursor_at\"`" + `
	ClosedAt    *string           ` + "`json:\"closed_at\"`" + `
	Secret      string            ` + "`json:\"-\"`" + `
	internal    string
}
`,
	})
	violations := wireMemberViolations(loaded, map[string]bool{"fixture/httpapi": true}, map[string]string{
		"fixture/httpapi.examResponse.cursor_at": "planted exemption",
	})
	expected := []string{
		`dto.go:8: fixture/httpapi.examResponse.ID json member "examId" must be snake_case`,
		`dto.go:9: fixture/httpapi.examResponse.UserID json member "user_Id" must be snake_case`,
		`dto.go:10: fixture/httpapi.examResponse.CreateAt json member "create_at" is retired; use created_at`,
		`dto.go:10: fixture/httpapi.examResponse.CreateAt json member "create_at" names an instant but has Go type int64; carry an RFC 3339 string`,
		`dto.go:11: fixture/httpapi.examResponse.UpdatedAt json member "updated_at" names an instant but has Go type int64; carry an RFC 3339 string`,
		`dto.go:12: fixture/httpapi.examResponse.ArchivedAt json member "archived_at" names an instant but has Go type int64; carry an RFC 3339 string`,
		`dto.go:13: fixture/httpapi.examResponse.ExpiresAt json member "expires_at" names an instant but has Go type int64; carry an RFC 3339 string`,
		`dto.go:14: fixture/httpapi.examResponse.PublishAt json member "publish_at" names an instant; use a <verb>ed_at lifecycle name or a scheduled noun form`,
		`dto.go:15: fixture/httpapi.examResponse.Untagged has no json tag; name the wire member explicitly or tag it "-"`,
		`dto.go:16: fixture/httpapi.examResponse.Unnamed has no json member name; name the wire member explicitly`,
		`dto.go:20: fixture/httpapi.examResponse.ClosedAt json member "closed_at" is an optional instant encoded as null when absent; tag it omitempty`,
	}
	require.Equal(t, expected, violations)
}

func wireMemberViolations(loaded []*packages.Package, transports map[string]bool, exemptions map[string]string) []string {
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
					violations = append(violations, checkWireMember(member, exemptions)...)
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

func checkWireMember(member wireMember, exemptions map[string]string) []string {
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
	if _, exempt := exemptions[member.pkg.PkgPath+"."+member.owner+"."+member.name]; exempt {
		return violations
	}
	if goType := member.pkg.TypesInfo.TypeOf(field.Type); goType != nil && !isWireTimestampType(goType) {
		violations = append(violations, fmt.Sprintf("%s json member %q names an instant but has Go type %s; carry an RFC 3339 string",
			location, member.name, types.TypeString(wireTimestampValue(goType), types.RelativeTo(member.pkg.Types))))
	}
	if goType := member.pkg.TypesInfo.TypeOf(field.Type); goType != nil && isPointerType(goType) && !hasJSONOption(member.options, "omitempty") {
		violations = append(violations, fmt.Sprintf("%s json member %q is an optional instant encoded as null when absent; tag it omitempty", location, member.name))
	}
	if _, retired := retiredWireTimestampNames[member.name]; !retired && strings.HasSuffix(member.name, "_at") && !conformingInstantName(member.name) {
		violations = append(violations, fmt.Sprintf("%s json member %q names an instant; use a <verb>ed_at lifecycle name or a scheduled noun form", location, member.name))
	}
	return violations
}

func isPointerType(goType types.Type) bool {
	_, pointer := types.Unalias(goType).(*types.Pointer)
	return pointer
}

func hasJSONOption(options, want string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == want {
			return true
		}
	}
	return false
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

// wireTimestampValue strips pointers and single-argument generic wrappers
// such as httpapi.Optional[T], which encode as their value.
func wireTimestampValue(goType types.Type) types.Type {
	for {
		goType = types.Unalias(goType)
		if pointer, ok := goType.(*types.Pointer); ok {
			goType = pointer.Elem()
			continue
		}
		if named, ok := goType.(*types.Named); ok && named.TypeArgs().Len() == 1 {
			goType = named.TypeArgs().At(0)
			continue
		}
		return goType
	}
}

// isWireTimestampType accepts a string, which transports format with
// model.FormatRFC3339, and time.Time, which encoding/json writes as RFC 3339
// for the canonical document mirrors that keep it.
func isWireTimestampType(goType types.Type) bool {
	value := wireTimestampValue(goType)
	if named, ok := value.(*types.Named); ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "time" && named.Obj().Name() == "Time" {
		return true
	}
	basic, ok := value.Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsString != 0
}
