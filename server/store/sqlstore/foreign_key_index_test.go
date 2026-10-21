// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/migrations"
)

// TestBaselineForeignKeysHaveLeadingIndexes keeps every foreign key of the
// PostgreSQL baseline served by a btree index (primary key, unique
// constraint, or CREATE INDEX) whose leading columns are exactly the
// referencing columns, in any order. Each parent delete probes the
// referencing side, and lookups by the referencing columns need the same
// index. A foreign key that deliberately has no such index is listed in
// unindexedForeignKeys with its reason, or in prefixServedForeignKeys with the
// column that leads the index serving it through a selective subset of its
// columns. Partial-index
// predicates are not judged here.
func TestBaselineForeignKeysHaveLeadingIndexes(t *testing.T) {
	t.Parallel()

	source, err := migrations.Assets.ReadFile("postgres/000001_baseline.up.sql")
	require.NoError(t, err)
	schema, err := parseSchemaKeys(string(source))
	require.NoError(t, err)
	require.NotEmpty(t, schema.foreignKeys)
	for _, violation := range foreignKeyIndexViolations(schema, unindexedForeignKeys, prefixServedForeignKeys) {
		assert.Fail(t, violation)
	}
}

func TestForeignKeyIndexRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()

	schema, err := parseSchemaKeys(`
-- a comment with a REFERENCES word; and a semicolon
CREATE TABLE parents (
    id varchar(26) PRIMARY KEY,
    name text NOT NULL CHECK (name <> 'x;y'),
    CONSTRAINT parents_id_name_key UNIQUE (id, name)
);
CREATE TABLE children (
    id varchar(26) PRIMARY KEY,
    parent_id varchar(26) NOT NULL REFERENCES parents(id),
    indexed_parent_id varchar(26) REFERENCES parents(id),
    pair_id varchar(26),
    pair_name text,
    gin_parent_id varchar(26) REFERENCES parents(id),
    expression_parent_id varchar(26) REFERENCES parents(id),
    allowed_parent_id varchar(26) REFERENCES parents(id),
    prefix_parent_id varchar(26),
    prefix_name text,
    stale_id varchar(26),
    wrong_prefix_id varchar(26) REFERENCES parents(id),
    FOREIGN KEY (pair_name, pair_id) REFERENCES parents(name, id),
    CONSTRAINT children_prefix_fkey FOREIGN KEY (prefix_parent_id, prefix_name) REFERENCES parents(id, name)
);
CREATE INDEX children_indexed_parent_id_idx ON children (indexed_parent_id) WHERE indexed_parent_id IS NOT NULL;
CREATE UNIQUE INDEX children_pair_key ON children USING btree (pair_id, pair_name DESC, id);
CREATE INDEX children_gin_parent_id_idx ON children USING gin (gin_parent_id gin_trgm_ops);
CREATE INDEX children_expression_parent_id_idx ON children (lower(expression_parent_id), expression_parent_id);
CREATE INDEX children_prefix_idx ON children (prefix_parent_id, id);
CREATE FUNCTION children_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    ALTER TABLE children ADD CONSTRAINT not_parsed FOREIGN KEY (id) REFERENCES parents(id);
    RETURN NEW;
END;
$$;
ALTER TABLE parents ADD CONSTRAINT parents_self_fkey FOREIGN KEY (name, id) REFERENCES parents(name, id);
`)
	require.NoError(t, err)
	violations := foreignKeyIndexViolations(schema,
		map[foreignKeyColumns]string{
			{"children", "allowed_parent_id"}: "planted decision",
			{"children", "stale_id"}:          "planted stale decision",
			{"children", "indexed_parent_id"}: "planted redundant decision",
		},
		map[foreignKeyColumns]string{
			{"children", "prefix_parent_id,prefix_name"}: "prefix_parent_id",
			{"children", "parent_id"}:                    "id",
			{"children", "pair_name,pair_id"}:            "pair_id",
			{"children", "wrong_prefix_id"}:              "wrong_prefix_id",
		},
	)
	expected := []string{
		"children(expression_parent_id) references parents and has no btree index led by exactly its columns",
		"children(gin_parent_id) references parents and has no btree index led by exactly its columns",
		"unindexedForeignKeys lists children(indexed_parent_id), which is now served by an index; remove the entry",
		"prefixServedForeignKeys lists children(pair_name,pair_id), which is now served by an exact index; remove the entry",
		"unindexedForeignKeys lists children(stale_id), which is no longer a foreign key; remove the entry",
		"prefixServedForeignKeys lists children(parent_id) as served by an index led by id, which is not one of its columns",
		"prefixServedForeignKeys lists children(wrong_prefix_id) as served by an index led by wrong_prefix_id, and no btree index leads with it",
	}
	require.Equal(t, expected, violations)
}

// foreignKeyColumns names a foreign key by its referencing table and its
// referencing columns in declaration order.
type foreignKeyColumns struct {
	table   string
	columns string
}

func (k foreignKeyColumns) String() string { return k.table + "(" + k.columns + ")" }

type schemaForeignKey struct {
	foreignKeyColumns
	referenced string
}

type schemaIndex struct {
	name    string
	table   string
	method  string
	columns []string // "" marks an expression, which ends the usable prefix
}

type schemaKeys struct {
	foreignKeys []schemaForeignKey
	indexes     []schemaIndex
}

func foreignKeyIndexViolations(schema schemaKeys, unindexed, prefixServed map[foreignKeyColumns]string) []string {
	var missing, stale []string
	declared := make(map[foreignKeyColumns]bool)
	for _, key := range schema.foreignKeys {
		declared[key.foreignKeyColumns] = true
		columns := strings.Split(key.columns, ",")
		covered := false
		for _, index := range schema.indexes {
			if index.table == key.table && index.method == "btree" && leadsWithExactly(index.columns, columns) {
				covered = true
				break
			}
		}
		_, decided := unindexed[key.foreignKeyColumns]
		_, served := prefixServed[key.foreignKeyColumns]
		switch {
		case covered && decided:
			stale = append(stale, fmt.Sprintf("unindexedForeignKeys lists %s, which is now served by an index; remove the entry", key.foreignKeyColumns))
		case covered && served:
			stale = append(stale, fmt.Sprintf("prefixServedForeignKeys lists %s, which is now served by an exact index; remove the entry", key.foreignKeyColumns))
		case !covered && !decided && !served:
			missing = append(missing, fmt.Sprintf("%s references %s and has no btree index led by exactly its columns", key.foreignKeyColumns, key.referenced))
		}
	}
	for _, key := range sortedForeignKeyColumns(unindexed) {
		if !declared[key] {
			stale = append(stale, fmt.Sprintf("unindexedForeignKeys lists %s, which is no longer a foreign key; remove the entry", key))
		}
	}
	for _, key := range sortedForeignKeyColumns(prefixServed) {
		if !declared[key] {
			stale = append(stale, fmt.Sprintf("prefixServedForeignKeys lists %s, which is no longer a foreign key; remove the entry", key))
			continue
		}
		leading := prefixServed[key]
		if !slices.Contains(strings.Split(key.columns, ","), leading) {
			stale = append(stale, fmt.Sprintf("prefixServedForeignKeys lists %s as served by an index led by %s, which is not one of its columns", key, leading))
			continue
		}
		leads := slices.ContainsFunc(schema.indexes, func(index schemaIndex) bool {
			return index.table == key.table && index.method == "btree" && len(index.columns) > 0 && index.columns[0] == leading
		})
		if !leads {
			stale = append(stale, fmt.Sprintf("prefixServedForeignKeys lists %s as served by an index led by %s, and no btree index leads with it", key, leading))
		}
	}
	sort.Strings(missing)
	return append(missing, stale...)
}

func leadsWithExactly(indexColumns, foreignKeyColumns []string) bool {
	if len(indexColumns) < len(foreignKeyColumns) {
		return false
	}
	leading := slices.Clone(indexColumns[:len(foreignKeyColumns)])
	wanted := slices.Clone(foreignKeyColumns)
	slices.Sort(leading)
	slices.Sort(wanted)
	return !slices.Contains(leading, "") && slices.Equal(leading, wanted)
}

func sortedForeignKeyColumns(keys map[foreignKeyColumns]string) []foreignKeyColumns {
	sorted := make([]foreignKeyColumns, 0, len(keys))
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	return sorted
}

// parseSchemaKeys reads the foreign keys and indexes that CREATE TABLE,
// CREATE INDEX, and ALTER TABLE ... ADD statements declare. Function bodies
// are dollar-quoted and skipped whole.
func parseSchemaKeys(source string) (schemaKeys, error) {
	tokens, err := sqlTokens(source)
	if err != nil {
		return schemaKeys{}, err
	}
	var schema schemaKeys
	for _, statement := range splitSQL(tokens, ";") {
		switch {
		case matchTokens(statement, "create", "table"):
			err = parseCreateTable(&schema, statement[2:])
		case matchTokens(statement, "create", "index"), matchTokens(statement, "create", "unique", "index"):
			err = parseCreateIndex(&schema, statement)
		case matchTokens(statement, "alter", "table"):
			err = parseAlterTable(&schema, statement[2:])
		}
		if err != nil {
			return schemaKeys{}, fmt.Errorf("%s: %w", strings.Join(statement[:min(len(statement), 6)], " "), err)
		}
	}
	return schema, nil
}

func parseCreateTable(schema *schemaKeys, statement []string) error {
	statement = skipTokens(statement, "if", "not", "exists")
	if len(statement) < 2 || statement[1] != "(" {
		return fmt.Errorf("expected a table body")
	}
	table := statement[0]
	body, _, err := parenthesized(statement[1:])
	if err != nil {
		return err
	}
	for _, element := range splitSQL(body, ",") {
		if len(element) == 0 {
			continue
		}
		if err := parseTableElement(schema, table, element); err != nil {
			return err
		}
	}
	return nil
}

func parseTableElement(schema *schemaKeys, table string, element []string) error {
	name := ""
	if element[0] == "constraint" && len(element) > 2 {
		name, element = element[1], element[2:]
	}
	switch {
	case matchTokens(element, "primary", "key"):
		return addConstraintIndex(schema, table, name, element[2:])
	case element[0] == "unique":
		return addConstraintIndex(schema, table, name, skipTokens(element[1:], "nulls", "not", "distinct"))
	case matchTokens(element, "foreign", "key"):
		return addForeignKey(schema, table, element[2:])
	case element[0] == "check", element[0] == "exclude":
		return nil
	case name != "":
		return fmt.Errorf("unsupported constraint %q", element[0])
	}
	// A column definition: column-level PRIMARY KEY, UNIQUE, and REFERENCES
	// appear outside parentheses.
	column := element[0]
	depth := 0
	for i := 1; i < len(element); i++ {
		switch element[i] {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth != 0 {
			continue
		}
		switch {
		case matchTokens(element[i:], "primary", "key"), element[i] == "unique":
			schema.indexes = append(schema.indexes, schemaIndex{name: table + "." + column, table: table, method: "btree", columns: []string{column}})
		case element[i] == "references" && i+1 < len(element):
			schema.foreignKeys = append(schema.foreignKeys, schemaForeignKey{
				foreignKeyColumns: foreignKeyColumns{table: table, columns: column}, referenced: element[i+1],
			})
		}
	}
	return nil
}

func addConstraintIndex(schema *schemaKeys, table, name string, rest []string) error {
	columns, _, err := parenthesized(rest)
	if err != nil {
		return err
	}
	if name == "" {
		name = table + "." + strings.Join(columns, "_")
	}
	index := schemaIndex{name: name, table: table, method: "btree"}
	for _, column := range splitSQL(columns, ",") {
		index.columns = append(index.columns, indexColumn(column))
	}
	schema.indexes = append(schema.indexes, index)
	return nil
}

func addForeignKey(schema *schemaKeys, table string, rest []string) error {
	columns, after, err := parenthesized(rest)
	if err != nil {
		return err
	}
	if len(after) < 2 || after[0] != "references" {
		return fmt.Errorf("foreign key on %s has no REFERENCES clause", table)
	}
	var names []string
	for _, column := range splitSQL(columns, ",") {
		names = append(names, strings.Join(column, " "))
	}
	schema.foreignKeys = append(schema.foreignKeys, schemaForeignKey{
		foreignKeyColumns: foreignKeyColumns{table: table, columns: strings.Join(names, ",")}, referenced: after[1],
	})
	return nil
}

func parseCreateIndex(schema *schemaKeys, statement []string) error {
	statement = skipTokens(statement, "create")
	statement = skipTokens(statement, "unique")
	statement = skipTokens(statement[1:], "concurrently")
	statement = skipTokens(statement, "if", "not", "exists")
	if len(statement) < 3 || statement[1] != "on" {
		return fmt.Errorf("expected an index name and ON")
	}
	index := schemaIndex{name: statement[0], method: "btree"}
	statement = skipTokens(statement[2:], "only")
	index.table, statement = statement[0], statement[1:]
	if len(statement) > 1 && statement[0] == "using" {
		index.method, statement = statement[1], statement[2:]
	}
	elements, _, err := parenthesized(statement)
	if err != nil {
		return err
	}
	for _, element := range splitSQL(elements, ",") {
		index.columns = append(index.columns, indexColumn(element))
	}
	schema.indexes = append(schema.indexes, index)
	return nil
}

func parseAlterTable(schema *schemaKeys, statement []string) error {
	statement = skipTokens(statement, "if", "exists")
	statement = skipTokens(statement, "only")
	if len(statement) == 0 {
		return fmt.Errorf("expected a table name")
	}
	table := statement[0]
	for _, action := range splitSQL(statement[1:], ",") {
		if len(action) > 1 && action[0] == "add" && action[1] != "column" {
			if err := parseTableElement(schema, table, action[1:]); err != nil {
				return err
			}
		}
	}
	return nil
}

// indexColumn returns the column an index element names, or "" for an
// expression.
func indexColumn(element []string) string {
	if len(element) == 0 || !isSQLIdentifier(element[0]) || (len(element) > 1 && element[1] == "(") {
		return ""
	}
	return element[0]
}

// sqlTokens lowercases words, drops comments and whitespace, and keeps each
// quoted string or dollar-quoted body as one token.
func sqlTokens(source string) ([]string, error) {
	var tokens []string
	for i := 0; i < len(source); {
		c := source[i]
		switch {
		case unicode.IsSpace(rune(c)):
			i++
		case strings.HasPrefix(source[i:], "--"):
			end := strings.IndexByte(source[i:], '\n')
			if end < 0 {
				end = len(source) - i
			}
			i += end
		case strings.HasPrefix(source[i:], "/*"):
			end := strings.Index(source[i:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += end + 2
		case c == '\'' || c == '"':
			end := i + 1
			for end < len(source) {
				if source[end] == c {
					if end+1 < len(source) && source[end+1] == c {
						end += 2
						continue
					}
					break
				}
				end++
			}
			if end >= len(source) {
				return nil, fmt.Errorf("unterminated quoted text")
			}
			token := source[i : end+1]
			if c == '"' {
				token = strings.ReplaceAll(token[1:len(token)-1], `""`, `"`)
			}
			tokens = append(tokens, token)
			i = end + 1
		case c == '$' && dollarTag(source[i:]) != "":
			tag := dollarTag(source[i:])
			end := strings.Index(source[i+len(tag):], tag)
			if end < 0 {
				return nil, fmt.Errorf("unterminated dollar-quoted body")
			}
			tokens = append(tokens, "$body$")
			i += len(tag) + end + len(tag)
		case isSQLWordByte(c):
			end := i
			for end < len(source) && isSQLWordByte(source[end]) {
				end++
			}
			tokens = append(tokens, strings.ToLower(source[i:end]))
			i = end
		default:
			tokens = append(tokens, string(c))
			i++
		}
	}
	return tokens, nil
}

func dollarTag(source string) string {
	end := 1
	for end < len(source) && (source[end] == '_' || unicode.IsLetter(rune(source[end])) || (end > 1 && unicode.IsDigit(rune(source[end])))) {
		end++
	}
	if end < len(source) && source[end] == '$' {
		return source[:end+1]
	}
	return ""
}

func isSQLWordByte(c byte) bool {
	return c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isSQLIdentifier(token string) bool {
	return token != "" && (token[0] == '_' || (token[0] >= 'a' && token[0] <= 'z'))
}

// splitSQL splits tokens at a separator outside parentheses.
func splitSQL(tokens []string, separator string) [][]string {
	var parts [][]string
	start, depth := 0, 0
	for i, token := range tokens {
		switch token {
		case "(":
			depth++
		case ")":
			depth--
		case separator:
			if depth == 0 {
				parts = append(parts, tokens[start:i])
				start = i + 1
			}
		}
	}
	if start < len(tokens) {
		parts = append(parts, tokens[start:])
	}
	return parts
}

// parenthesized returns the tokens inside a leading parenthesized group and
// the tokens after it.
func parenthesized(tokens []string) ([]string, []string, error) {
	if len(tokens) == 0 || tokens[0] != "(" {
		return nil, nil, fmt.Errorf("expected (")
	}
	depth := 0
	for i, token := range tokens {
		switch token {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return tokens[1:i], tokens[i+1:], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("unbalanced parentheses")
}

func matchTokens(tokens []string, words ...string) bool {
	return len(tokens) >= len(words) && slices.Equal(tokens[:len(words)], words)
}

// skipTokens drops the leading words when the tokens start with all of them;
// "unique nulls not distinct" style optional words are skipped one by one.
func skipTokens(tokens []string, words ...string) []string {
	if matchTokens(tokens, words...) {
		return tokens[len(words):]
	}
	if len(words) == 3 && words[0] == "nulls" && matchTokens(tokens, "nulls", "distinct") {
		return tokens[2:]
	}
	return tokens
}
