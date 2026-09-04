// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package logging provides Proctor's bounded structured operational logger.
package logging

import (
	"fmt"
	"reflect"
	"time"

	logr "github.com/mattermost/logr/v2"
)

// Field can only be constructed through the bounded helpers in this package.
// The backend representation remains hidden so callers cannot smuggle maps,
// structs, or lazy serializers into operational logs.
type Field struct{ value logr.Field }

func String(key, value string) Field      { return Field{value: logr.String(key, value)} }
func Int(key string, value int) Field     { return Field{value: logr.Int(key, value)} }
func Int64(key string, value int64) Field { return Field{value: logr.Int(key, value)} }
func Bool(key string, value bool) Field   { return Field{value: logr.Bool(key, value)} }
func Duration(key string, value time.Duration) Field {
	return Field{value: logr.Duration(key, value)}
}
func Time(key string, value time.Time) Field { return Field{value: logr.Time(key, value)} }

// Any deliberately accepts only bounded scalar values. Arbitrary maps and
// structs are represented by type, preventing reflective serialization of
// secrets or unbounded user-controlled graphs.
func Any(key string, value any) Field {
	switch typed := value.(type) {
	case nil:
		return String(key, "<nil>")
	case string:
		return String(key, typed)
	case []byte:
		return String(key, string(typed))
	case bool:
		return Bool(key, typed)
	case int:
		return Int(key, typed)
	case int8:
		return Field{value: logr.Int(key, typed)}
	case int16:
		return Field{value: logr.Int(key, typed)}
	case int32:
		return Field{value: logr.Int(key, typed)}
	case int64:
		return Int64(key, typed)
	case uint:
		return Field{value: logr.Uint(key, typed)}
	case uint8:
		return Field{value: logr.Uint(key, typed)}
	case uint16:
		return Field{value: logr.Uint(key, typed)}
	case uint32:
		return Field{value: logr.Uint(key, typed)}
	case uint64:
		return Field{value: logr.Uint(key, typed)}
	case float32:
		return Field{value: logr.Float(key, typed)}
	case float64:
		return Field{value: logr.Float(key, typed)}
	case time.Time:
		return Time(key, typed)
	case time.Duration:
		return Duration(key, typed)
	case error:
		return Field{value: logr.NamedErr(key, typed)}
	default:
		name := "unknown"
		if reflected := reflect.TypeOf(value); reflected != nil {
			name = reflected.String()
		}
		return String(key, fmt.Sprintf("<unsupported %s>", name))
	}
}

func Err(err error) Field {
	if err == nil {
		return String("error", "<nil>")
	}
	return Field{value: logr.Err(err)}
}
