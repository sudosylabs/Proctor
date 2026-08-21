// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/store"
)

type relationshipTransitionMailPreparation = appmail.RelationshipTransitionPreparation

type relationshipTransitionMailPreparer interface {
	PrepareRelationshipTransition(relationshipTransitionMailPreparation) (*store.PreparedMail, error)
}
