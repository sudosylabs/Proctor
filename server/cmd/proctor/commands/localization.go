// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"os"
	"strings"
	"text/template"

	"github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/localization"
)

const commandLocalizationOrigin = "cmd/proctor/commands"

type commandLocalizer interface {
	Translate(locale, id string, args any) (string, error)
}

type commandText struct {
	localizer commandLocalizer
	locale    string
}

func (t commandText) value(id, fallback string, args any) string {
	if t.localizer != nil {
		if translated, err := t.localizer.Translate(t.locale, id, args); err == nil {
			return translated
		}
	}
	if args != nil {
		parsed, err := template.New(id).Option("missingkey=error").Parse(fallback)
		if err == nil {
			var rendered bytes.Buffer
			if err := parsed.Execute(&rendered, args); err == nil {
				return rendered.String()
			}
		}
	}
	return fallback
}

func productionCommandText() commandText {
	localizer, _ := server.NewEmbeddedLocalizer()
	if localizer == nil {
		return commandText{}
	}
	raw := firstNonemptyEnvironment("PROCTOR_LOCALE", "LC_ALL", "LC_MESSAGES", "LANG")
	if prefix, _, found := strings.Cut(raw, "."); found {
		raw = prefix
	}
	if prefix, _, found := strings.Cut(raw, "@"); found {
		raw = prefix
	}
	return commandText{
		localizer: localizer,
		locale:    localization.PreferredLocale(raw, localizer.SupportedLocales()),
	}
}

func firstNonemptyEnvironment(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

// LocalizationDefinitions returns all prose owned by the operator CLI.
func LocalizationDefinitions() []localization.Definition {
	plain := []string{
		"cli.administrator.error.invalid_flags",
		"cli.administrator.error.no_result",
		"cli.administrator.error.requires_recover",
		"cli.administrator.error.requires_recovery_input",
		"cli.administrator.error.requires_subcommand",
		"cli.administrator.error.unexpected_arguments",
		"cli.administrator.flag.enable_local_login",
		"cli.administrator.flag.institution_id",
		"cli.administrator.flag.rotate_password",
		"cli.administrator.flag.user_id",
		"cli.administrator.recover.short",
		"cli.administrator.recover.success",
		"cli.administrator.reset_mfa.arguments",
		"cli.administrator.reset_mfa.input",
		"cli.administrator.reset_mfa.invalid_flags",
		"cli.administrator.reset_mfa.no_result",
		"cli.administrator.reset_mfa.short",
		"cli.administrator.reset_mfa.success",
		"cli.administrator.reset_mfa.unavailable",
		"cli.administrator.short",
		"cli.cluster.error.requires_subcommand",
		"cli.cluster.short",
		"cli.cluster.status.discovery_header",
		"cli.cluster.status.discovery_title",
		"cli.cluster.status.expired",
		"cli.cluster.status.flag.json",
		"cli.cluster.status.live",
		"cli.cluster.status.none",
		"cli.cluster.status.serving_header",
		"cli.cluster.status.serving_title",
		"cli.cluster.status.short",
		"cli.cluster.status.truncated",
		"cli.config.error.read_flag",
		"cli.config.error.requires_subcommand",
		"cli.config.flag.path",
		"cli.config.short",
		"cli.config.show.flag.json",
		"cli.config.show.short",
		"cli.config.validate.short",
		"cli.config.validate.success",
		"cli.jobs.error.requires_subcommand",
		"cli.jobs.flag.json",
		"cli.jobs.list.flag.limit",
		"cli.jobs.list.flag.state",
		"cli.jobs.list.flag.type",
		"cli.jobs.list.header",
		"cli.jobs.list.more",
		"cli.jobs.list.none",
		"cli.jobs.list.short",
		"cli.jobs.short",
		"cli.jobs.show.attempt_header",
		"cli.jobs.show.attempts",
		"cli.jobs.show.available",
		"cli.jobs.show.completed",
		"cli.jobs.show.created",
		"cli.jobs.show.id",
		"cli.jobs.show.last_error",
		"cli.jobs.show.more_attempts",
		"cli.jobs.show.no_attempts",
		"cli.jobs.show.progress",
		"cli.jobs.show.short",
		"cli.jobs.show.started",
		"cli.jobs.show.state",
		"cli.jobs.show.type",
		"cli.jobs.show.updated",
		"cli.migrate.down.empty_schema",
		"cli.migrate.down.error.confirm",
		"cli.migrate.down.error.steps",
		"cli.migrate.down.flag.confirm",
		"cli.migrate.down.flag.steps",
		"cli.migrate.down.short",
		"cli.migrate.error.requires_subcommand",
		"cli.migrate.short",
		"cli.migrate.status.short",
		"cli.migrate.up.short",
		"cli.password.error.inspect_input",
		"cli.password.error.invalid_length",
		"cli.password.error.read_input",
		"cli.password.error.terminal_input",
		"cli.password.error.unavailable",
		"cli.root.error.command_required",
		"cli.root.short",
		"cli.root.warning.root_user",
		"cli.serve.short",
		"cli.version.flag.json",
		"cli.version.short",
	}
	definitions := make([]localization.Definition, 0, len(plain)+3)
	for _, id := range plain {
		definitions = append(definitions, localization.Definition{ID: id, Origin: commandLocalizationOrigin})
	}
	definitions = append(definitions,
		localization.Definition{ID: "cli.cluster.status.database_time", Origin: commandLocalizationOrigin, Variables: []string{"Time"}},
		localization.Definition{ID: "cli.cluster.status.schema", Origin: commandLocalizationOrigin, Variables: []string{"DatabaseVersion", "PendingMigrations", "ServerVersion"}},
		localization.Definition{ID: "cli.config.show.environment_overrides", Origin: commandLocalizationOrigin, Variables: []string{"Names"}},
		localization.Definition{ID: "cli.jobs.error.limit", Origin: commandLocalizationOrigin, Variables: []string{"Maximum"}},
		localization.Definition{ID: "cli.jobs.error.unknown_state", Origin: commandLocalizationOrigin, Variables: []string{"Known", "Value"}},
		localization.Definition{ID: "cli.jobs.error.unknown_type", Origin: commandLocalizationOrigin, Variables: []string{"Known", "Value"}},
		localization.Definition{ID: "cli.migrate.down.error.live_nodes", Origin: commandLocalizationOrigin, Variables: []string{"Nodes"}},
		localization.Definition{ID: "cli.migrate.down.plan", Origin: commandLocalizationOrigin, Variables: []string{"Count", "DatabaseVersion", "TargetVersion"}},
		localization.Definition{ID: "cli.migrate.down.success", Origin: commandLocalizationOrigin, Variables: []string{"Version"}},
		localization.Definition{ID: "cli.migrate.up.success", Origin: commandLocalizationOrigin, Variables: []string{"Version"}},
		localization.Definition{ID: "cli.migrate.status.success", Origin: commandLocalizationOrigin, Variables: []string{"DatabaseVersion", "PendingMigrations", "ServerVersion"}},
		localization.Definition{ID: "cli.version.output", Origin: commandLocalizationOrigin, Variables: []string{"BuildTime", "Commit", "GoVersion", "Version"}},
	)
	return definitions
}
