// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"encoding/json"
)

// Optional preserves omitted, explicit-null, and present PATCH values.
type Optional[T any] struct {
	value T
	set   bool
	null  bool
}

func (o *Optional[T]) UnmarshalJSON(encoded []byte) error {
	o.set = true
	if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		o.null = true
		var zero T
		o.value = zero
		return nil
	}
	o.null = false
	return json.Unmarshal(encoded, &o.value)
}

func (o Optional[T]) ValuePointer() *T {
	if !o.set || o.null {
		return nil
	}
	value := o.value
	return &value
}

func (o Optional[T]) IsSet() bool  { return o.set }
func (o Optional[T]) IsNull() bool { return o.set && o.null }
