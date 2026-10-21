//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// Retiring one Submission removes the retry outcomes that cite it. Each
// statement must reach audit_events through an index, so retirement cost does
// not grow with the audit table.
func TestRetentionRetirementOutcomeDeletesUseIndexes(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	ctx := context.Background()
	user := saveIntegrationUser(t, ctx, s, &model.User{Username: "retirement-plan", Email: "retirement-plan@example.edu", DisplayName: "Retirement Plan"})
	const auditEvents = 200000
	db := s.GetMaster().DB()
	// Every event cites identities under the result keys retirement looks up;
	// one in ten has a retry outcome of the matching operation.
	_, err := db.ExecContext(ctx, `INSERT INTO audit_events(id,created_at,updated_at,action,resource_type,resource_id,scope_type,scope_id,status,node_id,result)
 SELECT 'zz'||translate(lpad(i::text,24,'0'),'0123456789','ybndrfg8ej'),clock_timestamp(),clock_timestamp(),'exam.attempt.connect',
  CASE WHEN i%2=0 THEN 'submission' ELSE 'exam_sitting' END,'zz'||translate(lpad((i%20000)::text,24,'0'),'0123456789','kmcpqxot1u'),
  'institution',$1,'success','retirement-plan-test',
  jsonb_build_object('exam_attempt_id','zz'||translate(lpad((i%20000)::text,24,'0'),'0123456789','ndrfg8ejkm'),
   'workspace_id','zz'||translate(lpad((i%20000)::text,24,'0'),'0123456789','cpqxot1uwi'),
   'exam_attempt_workspace_id','zz'||translate(lpad((i%20000)::text,24,'0'),'0123456789','cpqxot1uwi'),
   'exam_submission_id','zz'||translate(lpad((i%20000)::text,24,'0'),'0123456789','kmcpqxot1u'),
   'stream_id','stream-'||(i%20000),'source_session_id',md5((i%20000)::text)::uuid::text,'family','native')
 FROM generate_series(1,$2::integer) i`, user.ID.String(), auditEvents)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO command_outcomes(user_id,operation,key_digest,fingerprint_version,fingerprint,outcome_version,outcome,original_audit_event_id,created_at,expires_at)
 SELECT $1,(ARRAY[$2,$3,$4,$5])[1+i%4],sha256(i::text::bytea),1,sha256(i::text::bytea),1,'{}',
  'zz'||translate(lpad((i*10)::text,24,'0'),'0123456789','ybndrfg8ej'),clock_timestamp(),clock_timestamp()+interval '1 day'
 FROM generate_series(1,$6::integer/10) i`, user.ID.String(), store.ExamAttemptConnectOperation, store.SecurityPreflightPrepareOperation,
		store.NativeDeliveryAppendOperation, store.DeliveryStopDetailsOperation, auditEvents)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `ANALYZE audit_events; ANALYZE command_outcomes`)
	require.NoError(t, err)

	attempt := "zz" + "ndrfg8ejkm"[7:8] + "nnnnnnnnnnnnnnnnnnnnnnn"
	submission := "zz" + "kmcpqxot1u"[7:8] + "kkkkkkkkkkkkkkkkkkkkkkk"
	native := []any{store.NativeDeliveryAppendOperation, store.NativeDeliveryGapsOperation, store.NativeDeliveryFinalOperation, store.NativeDeliverySummaryOperation, attempt}
	for _, test := range []struct {
		name  string
		query string
		args  []any
	}{
		{"preflight", preflightOutcomeRetirement, []any{store.SecurityPreflightPrepareOperation, store.SecurityPreflightReportOperation, attempt, attempt}},
		{"connect", connectOutcomeRetirement, []any{store.ExamAttemptConnectOperation, attempt}},
		{"native stream", streamOutcomeRetirement, native},
		{"browser source", sourceSessionOutcomeRetirement, native},
		{"delivery stop", deliveryStopOutcomeRetirement, []any{store.DeliveryStopDetailsOperation, attempt, "native"}},
		{"work", workOutcomeRetirement, []any{submission}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := db.BeginTxx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			plan, encoded := explainAnalyze(t, ctx, tx, test.query, test.args...)
			plan.Plan.walk(func(node planNode) {
				require.False(t, node.NodeType == "Seq Scan" && (node.RelationName == "audit_events" || node.RelationName == "command_outcomes"),
					"retirement scans %s: %s", node.RelationName, encoded)
			})
		})
	}
}
