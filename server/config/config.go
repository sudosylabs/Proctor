// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package config owns Proctor's deployment configuration and its lifecycle.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 1

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("duration must be a string such as \"15s\"")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", value, err)
	}
	d.Duration = parsed
	return nil
}

type Server struct {
	ListenAddress     string   `json:"listen_address"`
	PublicURL         string   `json:"public_url"`
	ReadHeaderTimeout Duration `json:"read_header_timeout"`
	ReadTimeout       Duration `json:"read_timeout"`
	WriteTimeout      Duration `json:"write_timeout"`
	IdleTimeout       Duration `json:"idle_timeout"`
	ShutdownTimeout   Duration `json:"shutdown_timeout"`
	MaxHeaderBytes    int      `json:"max_header_bytes"`
	MaxBodyBytes      int64    `json:"max_body_bytes"`
}

type LogTarget struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Level  string `json:"level"`
	Format string `json:"format"`
	File   string `json:"file,omitempty"`
}

type Log struct {
	MaxFieldBytes int         `json:"max_field_bytes"`
	Targets       []LogTarget `json:"targets"`
}

type Database struct {
	DataSource            string   `json:"data_source"`
	MaxOpenConnections    int      `json:"max_open_connections"`
	MaxIdleConnections    int      `json:"max_idle_connections"`
	ConnectionMaxLifetime Duration `json:"connection_max_lifetime"`
	ConnectionMaxIdleTime Duration `json:"connection_max_idle_time"`
	QueryTimeout          Duration `json:"query_timeout"`
	MigrationTimeout      Duration `json:"migration_timeout"`
}

type Config struct {
	Version  int      `json:"version"`
	Server   Server   `json:"server"`
	Database Database `json:"database"`
	Log      Log      `json:"log"`
}

func Default() Config {
	return Config{
		Version: SchemaVersion,
		Server: Server{
			ListenAddress:     "127.0.0.1:8065",
			PublicURL:         "http://localhost:8065",
			ReadHeaderTimeout: Duration{Duration: 10 * time.Second},
			ReadTimeout:       Duration{Duration: 30 * time.Second},
			WriteTimeout:      Duration{Duration: 30 * time.Second},
			IdleTimeout:       Duration{Duration: 2 * time.Minute},
			ShutdownTimeout:   Duration{Duration: 15 * time.Second},
			MaxHeaderBytes:    1 << 20,
			MaxBodyBytes:      1 << 20,
		},
		Database: Database{
			DataSource:            "postgres://proctor:proctor@127.0.0.1:15432/proctor?sslmode=disable",
			MaxOpenConnections:    50,
			MaxIdleConnections:    10,
			ConnectionMaxLifetime: Duration{Duration: time.Hour},
			ConnectionMaxIdleTime: Duration{Duration: 5 * time.Minute},
			QueryTimeout:          Duration{Duration: 10 * time.Second},
			MigrationTimeout:      Duration{Duration: 60 * time.Second},
		},
		Log: Log{
			MaxFieldBytes: 16 << 10,
			Targets: []LogTarget{{
				Name:   "console",
				Type:   "console",
				Level:  "info",
				Format: "text",
			}},
		},
	}
}

func (c Config) Clone() Config {
	cloned := c
	cloned.Log.Targets = append([]LogTarget(nil), c.Log.Targets...)
	return cloned
}

// Redacted returns a safe copy for display.
func (c Config) Redacted() Config {
	redacted := c.Clone()
	if redacted.Database.DataSource != "" {
		redacted.Database.DataSource = "[redacted]"
	}
	return redacted
}

func (c Config) RedactedJSON() ([]byte, error) {
	return json.MarshalIndent(c.Redacted(), "", "  ")
}

type FieldError struct {
	Field   string
	Message string
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	var builder strings.Builder
	builder.WriteString("configuration is invalid")
	for _, field := range e.Fields {
		builder.WriteString("; ")
		builder.WriteString(field.Field)
		builder.WriteString(": ")
		builder.WriteString(field.Message)
	}
	return builder.String()
}

func (c Config) Validate() error {
	var fields []FieldError
	add := func(field, message string) {
		fields = append(fields, FieldError{Field: field, Message: message})
	}

	if c.Version != SchemaVersion {
		add("version", fmt.Sprintf("must be %d", SchemaVersion))
	}
	validateListenAddress(c.Server.ListenAddress, add)
	validatePublicURL(c.Server.PublicURL, add)

	durations := []struct {
		field string
		value time.Duration
	}{
		{"server.read_header_timeout", c.Server.ReadHeaderTimeout.Duration},
		{"server.read_timeout", c.Server.ReadTimeout.Duration},
		{"server.write_timeout", c.Server.WriteTimeout.Duration},
		{"server.idle_timeout", c.Server.IdleTimeout.Duration},
		{"server.shutdown_timeout", c.Server.ShutdownTimeout.Duration},
	}
	for _, item := range durations {
		if item.value <= 0 {
			add(item.field, "must be greater than zero")
		}
	}
	if c.Server.MaxHeaderBytes < 1024 || c.Server.MaxHeaderBytes > 16<<20 {
		add("server.max_header_bytes", "must be between 1024 and 16777216")
	}
	if c.Server.MaxBodyBytes < 1024 || c.Server.MaxBodyBytes > 100<<20 {
		add("server.max_body_bytes", "must be between 1024 and 104857600")
	}

	validateDatabase(c.Database, add)

	if c.Log.MaxFieldBytes < 256 || c.Log.MaxFieldBytes > 1<<20 {
		add("log.max_field_bytes", "must be between 256 and 1048576")
	}
	if len(c.Log.Targets) == 0 {
		add("log.targets", "must contain at least one target")
	}
	names := make(map[string]struct{}, len(c.Log.Targets))
	for index, target := range c.Log.Targets {
		prefix := fmt.Sprintf("log.targets[%d]", index)
		if target.Name == "" {
			add(prefix+".name", "is required")
		} else if _, exists := names[target.Name]; exists {
			add(prefix+".name", "must be unique")
		} else {
			names[target.Name] = struct{}{}
		}
		switch strings.ToLower(target.Type) {
		case "console":
			if target.File != "" {
				add(prefix+".file", "must be empty for a console target")
			}
		case "file":
			if target.File == "" {
				add(prefix+".file", "is required for a file target")
			} else if strings.ContainsAny(target.File, "\x00\r\n") {
				add(prefix+".file", "contains invalid characters")
			}
		default:
			add(prefix+".type", "must be console or file")
		}
		switch strings.ToLower(target.Level) {
		case "trace", "debug", "info", "warn", "error":
		default:
			add(prefix+".level", "must be one of trace, debug, info, warn, error")
		}
		switch strings.ToLower(target.Format) {
		case "text", "json":
		default:
			add(prefix+".format", "must be text or json")
		}
	}

	if len(fields) != 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validateDatabase(database Database, add func(string, string)) {
	if strings.ContainsAny(database.DataSource, "\x00\r\n") {
		add("database.data_source", "contains invalid characters")
	} else {
		parsed, err := url.Parse(database.DataSource)
		if err != nil || parsed.Scheme != "postgres" || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
			add("database.data_source", "must be a PostgreSQL URL with host and database name")
		}
	}
	if database.MaxOpenConnections <= 0 {
		add("database.max_open_connections", "must be greater than zero")
	}
	if database.MaxIdleConnections < 0 || database.MaxIdleConnections > database.MaxOpenConnections {
		add("database.max_idle_connections", "must be between zero and max_open_connections")
	}
	for _, item := range []struct {
		field string
		value time.Duration
	}{
		{"database.connection_max_lifetime", database.ConnectionMaxLifetime.Duration},
		{"database.connection_max_idle_time", database.ConnectionMaxIdleTime.Duration},
		{"database.query_timeout", database.QueryTimeout.Duration},
		{"database.migration_timeout", database.MigrationTimeout.Duration},
	} {
		if item.value <= 0 {
			add(item.field, "must be greater than zero")
		}
	}
}

func validateListenAddress(address string, add func(string, string)) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		add("server.listen_address", "must be a host:port TCP address")
		return
	}
	if strings.ContainsAny(host, "\r\n") {
		add("server.listen_address", "host contains invalid characters")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		add("server.listen_address", "port must be between 0 and 65535")
	}
}

func validatePublicURL(raw string, add func(string, string)) {
	parsed, err := url.Parse(raw)
	if err != nil {
		add("server.public_url", "must be an absolute HTTP or HTTPS URL")
		return
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		add("server.public_url", "scheme must be http or https")
	}
	if parsed.Host == "" {
		add("server.public_url", "host is required")
	}
	if parsed.User != nil {
		add("server.public_url", "user information is forbidden")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		add("server.public_url", "query and fragment are forbidden")
	}
}
