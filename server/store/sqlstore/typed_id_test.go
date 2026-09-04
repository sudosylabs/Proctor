// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestNullEntityIDScanAndValue(t *testing.T) {
	t.Parallel()

	var n NullEntityID
	if err := n.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if n.Valid {
		t.Fatal("nil scan should be invalid")
	}
	value, err := n.Value()
	if err != nil || value != nil {
		t.Fatalf("Value() = %v, %v", value, err)
	}

	id := model.NewId()
	if err := n.Scan(id); err != nil {
		t.Fatal(err)
	}
	if !n.Valid || n.ID != id {
		t.Fatalf("scan = %#v", n)
	}
	value, err = n.Value()
	if err != nil || value != id {
		t.Fatalf("Value() = %v, %v", value, err)
	}
	if err := n.Scan("bad"); err == nil {
		t.Fatal("invalid id should fail scan")
	}
}

func TestScanUserID(t *testing.T) {
	t.Parallel()

	raw := model.NewId()
	id, err := ScanUserID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if id.String() != raw {
		t.Fatalf("ScanUserID() = %q", id)
	}
	if _, err := ScanUserID(nil); err == nil {
		t.Fatal("null required id should fail")
	}
	value, err := UserIDValue(id)
	if err != nil || value != raw {
		t.Fatalf("UserIDValue() = %v, %v", value, err)
	}
	if _, err := UserIDValue(""); err == nil {
		t.Fatal("invalid typed id must fail Value")
	}
}

func TestTimeBoundaryHelpers(t *testing.T) {
	t.Parallel()

	absent := model.OptionalTime{}
	if absent.Valid {
		t.Fatal("zero optional time is absent")
	}
	present := model.OptionalTimeFromMillis(1_700_000_000_000)
	if !present.Valid || present.Millis() != 1_700_000_000_000 {
		t.Fatalf("%#v", present)
	}
	nullTime := NullTimeFromOptional(present)
	if !nullTime.Valid {
		t.Fatal("expected valid NullTime")
	}
	back := OptionalTimeFromNullTime(nullTime)
	if !back.Valid || back.Millis() != present.Millis() {
		t.Fatalf("%#v", back)
	}
}
