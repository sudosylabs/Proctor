// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package apperror

import "slices"

// Concealment declares how one family of protected use cases presents its
// failures. Present passes application errors through and reports anything
// else as Unavailable. Conceal also replaces a denial, or any code listed in
// Concealed, with resource.not_found, so a caller cannot tell a missing
// resource from one it may not see.
type Concealment struct {
	Unavailable Code
	Concealed   []Code
}

// Present returns err as the use case's public failure without concealing
// existence. A nil err stays nil.
func (c Concealment) Present(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := As(err); !ok {
		return New(c.Unavailable).Wrap(err)
	}
	return err
}

// Conceal is Present for a use case whose target's existence is protected.
func (c Concealment) Conceal(err error) error {
	err = c.Present(err)
	failure, ok := As(err)
	if !ok {
		return err
	}
	code := failure.Code()
	if code == "authorization.denied" || slices.ContainsFunc(c.Concealed, func(concealed Code) bool { return string(concealed) == code }) {
		return New("resource.not_found").Wrap(err)
	}
	return err
}
