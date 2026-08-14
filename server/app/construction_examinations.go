// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
)

func constructExaminations(deps Dependencies, foundation applicationFoundation, access accessAcademicConstruction) (examinationConstruction, error) {
	effects := examRealtimeEffects{realtime: foundation.realtime}
	authoring, err := examengine.NewAuthoring(
		deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(), deps.Store.User(),
		examAuthorizationAdapter{authorization: access.authorization},
		examAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		effects, effects, time.Now, model.NewExamID,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	return examinationConstruction{authoring: authoring}, nil
}
