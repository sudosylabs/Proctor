// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
)

// Typed-ID SQL adapters live at the persistence boundary. Domain types do not
// import database/sql; rows convert through these helpers until aggregates
// adopt typed IDs end to end.

// NullEntityID is a nullable textual ID column that validates the shared
// z-base-32 representation when present.
type NullEntityID struct {
	ID    string
	Valid bool
}

// Scan implements sql.Scanner for text/byte ID columns.
func (n *NullEntityID) Scan(value any) error {
	if n == nil {
		return fmt.Errorf("sqlstore: NullEntityID scan target is nil")
	}
	if value == nil {
		n.ID = ""
		n.Valid = false
		return nil
	}
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("sqlstore: cannot scan %T into entity id", value)
	}
	if text == "" {
		n.ID = ""
		n.Valid = false
		return nil
	}
	if !model.IsValidId(text) {
		return fmt.Errorf("sqlstore: scanned invalid entity id %q", text)
	}
	n.ID = text
	n.Valid = true
	return nil
}

// Value implements driver.Valuer.
func (n NullEntityID) Value() (driver.Value, error) {
	if !n.Valid || n.ID == "" {
		return nil, nil
	}
	if !model.IsValidId(n.ID) {
		return nil, fmt.Errorf("sqlstore: cannot store invalid entity id %q", n.ID)
	}
	return n.ID, nil
}

// EntityIDValue returns a driver value for a required ID string.
func EntityIDValue(id string) (driver.Value, error) {
	if !model.IsValidId(id) {
		return nil, fmt.Errorf("sqlstore: cannot store invalid entity id %q", id)
	}
	return id, nil
}

// ScanEntityID scans a required non-null ID column.
func ScanEntityID(value any) (string, error) {
	var n NullEntityID
	if err := n.Scan(value); err != nil {
		return "", err
	}
	if !n.Valid {
		return "", fmt.Errorf("sqlstore: entity id column is null")
	}
	return n.ID, nil
}

// UserIDValue encodes a typed user ID for SQL parameters.
func UserIDValue(id model.UserID) (driver.Value, error) {
	if !id.IsValid() {
		return nil, fmt.Errorf("sqlstore: cannot store invalid user id %q", id)
	}
	return id.String(), nil
}

// ScanUserID scans a required user ID column into a typed identifier.
func ScanUserID(value any) (model.UserID, error) {
	raw, err := ScanEntityID(value)
	if err != nil {
		return "", err
	}
	return model.ParseUserID(raw)
}

// ScanNullUserID scans a nullable user ID column.
func ScanNullUserID(value any) (model.UserID, bool, error) {
	var n NullEntityID
	if err := n.Scan(value); err != nil {
		return "", false, err
	}
	if !n.Valid {
		return "", false, nil
	}
	id, err := model.ParseUserID(n.ID)
	return id, true, err
}

// InstitutionIDValue encodes a typed institution ID for SQL parameters.
func InstitutionIDValue(id model.InstitutionID) (driver.Value, error) {
	if !id.IsValid() {
		return nil, fmt.Errorf("sqlstore: cannot store invalid institution id %q", id)
	}
	return id.String(), nil
}

// ScanInstitutionID scans a required institution ID column.
func ScanInstitutionID(value any) (model.InstitutionID, error) {
	raw, err := ScanEntityID(value)
	if err != nil {
		return "", err
	}
	return model.ParseInstitutionID(raw)
}

// AcademicUnitIDValue encodes a typed academic-unit ID for SQL parameters.
func AcademicUnitIDValue(id model.AcademicUnitID) (driver.Value, error) {
	if !id.IsValid() {
		return nil, fmt.Errorf("sqlstore: cannot store invalid academic unit id %q", id)
	}
	return id.String(), nil
}

// ScanAcademicUnitID scans a required academic-unit ID column.
func ScanAcademicUnitID(value any) (model.AcademicUnitID, error) {
	raw, err := ScanEntityID(value)
	if err != nil {
		return "", err
	}
	return model.ParseAcademicUnitID(raw)
}

// ClassIDValue encodes a typed class ID for SQL parameters.
func ClassIDValue(id model.ClassID) (driver.Value, error) {
	if !id.IsValid() {
		return nil, fmt.Errorf("sqlstore: cannot store invalid class id %q", id)
	}
	return id.String(), nil
}

// ScanClassID scans a required class ID column.
func ScanClassID(value any) (model.ClassID, error) {
	raw, err := ScanEntityID(value)
	if err != nil {
		return "", err
	}
	return model.ParseClassID(raw)
}

// Ensure NullEntityID implements the database interfaces.
var (
	_ sql.Scanner   = (*NullEntityID)(nil)
	_ driver.Valuer = NullEntityID{}
)
