// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package model defines Proctor's durable domain contracts, validation, and
// safe audit representations.
//
// The academic hierarchy separates organizational ownership, curriculum, and
// time-bound student cohorts:
//
//	Institution
//	└── AcademicUnit (a tree of faculties, schools, and departments)
//	    └── Programme
//	        └── ProgrammeLevel
//	            └── Class ── AcademicPeriod
//
// An Institution is the school or university represented by one installation.
// AcademicUnit forms an arbitrary-depth organizational tree: for example, a
// university can contain a College of Engineering, which contains a School of
// Computing. A Programme is a course of study owned by one of those units,
// such as Bachelor of Computer Science. ProgrammeLevel is a curriculum stage,
// such as Year 1. Class is the concrete roster into which students enroll at
// that stage during an AcademicPeriod, such as "Computer Science Year 1 -
// Class A" in the 2026-2027 academic year.
//
// This separation permits one programme level to have several classes in the
// same period, and permits new classes to be created in a later period without
// rewriting the programme. A student's organizational and programme placement
// is derived from the student's active ClassMember. Teachers and staff are
// associated with AcademicUnit through AcademicUnitMember and receive access
// through scoped RoleBinding values; membership itself is not authorization.
//
// Models in this package own shape-level invariants. Invariants that need
// authoritative state, such as hierarchy cycle detection or uniqueness, belong
// to the application and persistence layers.
//
// Domain primitives:
//
//   - entity-specific IDs (UserID, ClassID, …) share the opaque 26-character
//     z-base-32 representation but are not freely assignable across entities;
//   - durable aggregate times are UTC time.Time values, with OptionalTime for
//     nullable instants. Transport and legacy command boundaries perform any
//     required integer-millisecond conversion explicitly.
package model
