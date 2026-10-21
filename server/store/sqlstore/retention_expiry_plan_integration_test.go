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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// planNode is the subset of an EXPLAIN (ANALYZE, FORMAT JSON) node that the
// bounded-plan tests inspect.
type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexName    string     `json:"Index Name"`
	ActualLoops  float64    `json:"Actual Loops"`
	ActualRows   float64    `json:"Actual Rows"`
	Plans        []planNode `json:"Plans"`
}

type planDocument struct {
	Plan          planNode `json:"Plan"`
	ExecutionTime float64  `json:"Execution Time"`
}

func (n planNode) walk(visit func(planNode)) {
	visit(n)
	for _, child := range n.Plans {
		child.walk(visit)
	}
}

// planQueryer is a raw connection or transaction, which bypasses the Store
// query timeout so a slow plan reports its shape instead of failing.
type planQueryer interface {
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	Rebind(query string) string
}

// explainAnalyze runs one statement under EXPLAIN ANALYZE. A data-changing
// statement takes effect, so callers pass a transaction they roll back.
func explainAnalyze(t *testing.T, ctx context.Context, q planQueryer, query string, args ...any) (planDocument, string) {
	t.Helper()
	var encoded []byte
	require.NoError(t, q.GetContext(ctx, &encoded, q.Rebind(`EXPLAIN (ANALYZE, FORMAT JSON) `+query), args...))
	var documents []planDocument
	require.NoError(t, json.Unmarshal(encoded, &documents), "decode plan: %s", encoded)
	require.Len(t, documents, 1, "plan documents: %s", encoded)
	return documents[0], string(encoded)
}

type expiryReasonCount struct {
	Reason string `db:"reason"`
	Count  int64  `db:"count"`
}

// requireAuditExpiryCountsMatchFacts proves the set-based preview count
// classifies every audit event exactly as the per-event facts that
// reconciliation and paging use. Inside one rolled-back transaction it adds
// active holds and source protections at each scope and ages half of the
// events, so every blocker the fixture can reach is compared.
func requireAuditExpiryCountsMatchFacts(t *testing.T, s *SQLStore) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.GetMaster().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	compare := func(stage string) {
		t.Helper()
		var at time.Time
		require.NoError(t, tx.Get(ctx, &at, `SELECT clock_timestamp()`))
		for _, days := range []int{0, 30} {
			policy := &model.RetentionPolicy{RetentionPolicySettings: model.RetentionPolicySettings{AuditRetentionDays: days}}
			var perEvent, setBased []expiryReasonCount
			require.NoError(t, tx.Select(ctx, &perEvent, expiryClassificationQuery(`facts AS (`+auditExpiryFacts+`)`), expiryCountArguments(policy, at)...))
			require.NoError(t, tx.Select(ctx, &setBased, expiryCountQuery(model.RetentionExpiryAudit), expiryCountArguments(policy, at)...))
			require.NotEmpty(t, perEvent, "audit fixture is empty")
			require.ElementsMatch(t, perEvent, setBased, "%s with audit retention days %d", stage, days)
		}
	}
	compare("fixture")
	_, err = tx.Exec(ctx, `UPDATE audit_events SET created_at=created_at-interval '100 days',updated_at=updated_at-interval '100 days' WHERE id IN (SELECT id FROM audit_events ORDER BY id LIMIT (SELECT count(*)/2 FROM audit_events))`)
	require.NoError(t, err)
	compare("aged")
	_, err = tx.Exec(ctx, `WITH source AS (SELECT sub.id,a.exam_id,a.exam_sitting_id FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id ORDER BY sub.id LIMIT 1),
  export AS (INSERT INTO exam_exports(id,exam_id,exam_sitting_id,submission_id,requester_user_id,categories,state,policy_revision,created_at,expires_at,source_expires_at,submission_count,file_count,source_bytes,job_id)
   SELECT 'zzexportexportexportexport',exam_id,exam_sitting_id,id,(SELECT id FROM users ORDER BY id LIMIT 1),ARRAY['work'],'failed',1,clock_timestamp(),
    clock_timestamp()+interval '1 day',clock_timestamp()+interval '1 hour',1,0,0,'zzexportjobexportjobexport' FROM source RETURNING id,submission_id)
 INSERT INTO retention_source_protections(export_id,submission_id,category,created_at,expires_at)
 SELECT id,submission_id,'work',clock_timestamp(),clock_timestamp()+interval '1 hour' FROM export`)
	require.NoError(t, err)
	compare("source protected")
	_, err = tx.Exec(ctx, `INSERT INTO retention_holds(id,exam_id,exam_sitting_id,submission_id,revision,created_at,created_by_user_id,reason_code,private_reason,creation_audit_event_id)
 SELECT 'zzhq'||translate(lpad(n::text,22,'0'),'0123456789','ybndrfg8ej'),scope.exam_id,scope.sitting_id,scope.submission_id,1,clock_timestamp(),
  (SELECT id FROM users ORDER BY id LIMIT 1),'other','equivalence',(SELECT id FROM audit_events ORDER BY id DESC LIMIT 1)
 FROM (SELECT row_number() OVER () AS n,* FROM (
  (SELECT a.exam_id,a.exam_sitting_id AS sitting_id,sub.id AS submission_id FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id ORDER BY sub.id LIMIT 1)
  UNION ALL (SELECT exam_id,id,NULL FROM exam_sittings ORDER BY id DESC LIMIT 1)
  UNION ALL (SELECT id,NULL,NULL FROM exams ORDER BY id DESC LIMIT 1)) scopes) scope`)
	require.NoError(t, err)
	compare("held")
}

func TestRetentionExpiryAuditCountMatchesFacts(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	storetest.TestRetentionReceiptExpiryStore(t, s, retentionSQLProbe(s), retentionExpirySQLProbe(s))
	requireAuditExpiryCountsMatchFacts(t, s)
	resetTestStore(t, s)
	storetest.TestRetentionAuditUnfinishedExamScope(t, s, retentionExpirySQLProbe(s))
	requireAuditExpiryCountsMatchFacts(t, s)
	resetPristineTestStore(t, s)
	storetest.TestRetentionExpiryStore(t, s, retentionExpirySQLProbe(s))
	requireAuditExpiryCountsMatchFacts(t, s)
}

// A retention preview counts every audit event inside one statement bounded by
// the Store query timeout. Its plan must scan the audit table a constant
// number of times and never repeat work per audit event.
func TestRetentionExpiryAuditCountPlanIsBounded(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	// The receipt scenario leaves Exams, Sittings, Submissions, holds,
	// receipts and command outcomes behind for the synthetic events to cite.
	storetest.TestRetentionReceiptExpiryStore(t, s, retentionSQLProbe(s), retentionExpirySQLProbe(s))
	ctx := context.Background()
	const auditEvents = 200000
	db := s.GetMaster().DB()
	_, err := db.ExecContext(ctx, `WITH resources AS (
   SELECT row_number() OVER (ORDER BY resource_type,id)-1 AS n,resource_type,id FROM (
    SELECT 'exam' AS resource_type,id FROM exams UNION ALL SELECT 'exam_sitting',id FROM exam_sittings
    UNION ALL SELECT 'submission',id FROM exam_submissions) cited),
  total AS (SELECT count(*) AS c FROM resources),
  institution AS (SELECT id FROM institutions LIMIT 1)
 INSERT INTO audit_events(id,created_at,updated_at,action,resource_type,resource_id,scope_type,scope_id,status,node_id)
 SELECT 'zz'||translate(lpad(i::text,24,'0'),'0123456789','ybndrfg8ej'),
  clock_timestamp()-make_interval(mins=>i),clock_timestamp()-make_interval(mins=>i),
  CASE WHEN i%50=0 THEN 'retention_cleanup.manage' ELSE 'exam.manage' END,
  CASE i%4 WHEN 0 THEN r.resource_type WHEN 1 THEN 'submission' ELSE 'institution' END,
  CASE i%4 WHEN 0 THEN r.id WHEN 1 THEN 'zz'||translate(lpad((i%5000)::text,24,'0'),'0123456789','kmcpqxot1u') ELSE institution.id END,
  'institution',institution.id,CASE WHEN i%97=0 THEN 'attempt' ELSE 'success' END,'retention-plan-test'
 FROM generate_series(1,$1::integer) i CROSS JOIN total CROSS JOIN institution JOIN resources r ON r.n=i%total.c`, auditEvents)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO command_outcomes(user_id,operation,key_digest,fingerprint_version,fingerprint,outcome_version,outcome,original_audit_event_id,created_at,expires_at)
 SELECT (SELECT id FROM users ORDER BY id LIMIT 1),'retention.plan.reference',sha256(i::text::bytea),1,sha256(i::text::bytea),1,'{}',
  'zz'||translate(lpad((i*10)::text,24,'0'),'0123456789','ybndrfg8ej'),clock_timestamp(),clock_timestamp()+interval '1 day'
 FROM generate_series(1,$1::integer/10) i`, auditEvents)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `ANALYZE`)
	require.NoError(t, err)

	var at time.Time
	require.NoError(t, s.GetMaster().Get(ctx, &at, `SELECT clock_timestamp()`))
	policy := &model.RetentionPolicy{RetentionPolicySettings: model.RetentionPolicySettings{AuditRetentionDays: 30}}
	plan, encoded := explainAnalyze(t, ctx, s.GetMaster().DB(), expiryCountQuery(model.RetentionExpiryAudit), expiryCountArguments(policy, at)...)
	auditScans := 0
	plan.Plan.walk(func(node planNode) {
		// A node repeated per audit event is the correlated-subquery shape that
		// made the preview outlive the query timeout.
		require.Less(t, node.ActualLoops, float64(auditEvents/20), "%s on %q repeats per audit event: %s", node.NodeType, node.RelationName, encoded)
		if node.NodeType == "Seq Scan" && (node.RelationName == "audit_events" || node.RelationName == "command_outcomes") {
			require.LessOrEqual(t, node.ActualLoops, float64(1), "%s on %s repeats: %s", node.NodeType, node.RelationName, encoded)
			if node.RelationName == "audit_events" {
				auditScans++
			}
		}
	})
	require.LessOrEqual(t, auditScans, 2, "audit table scans: %s", encoded)
	// Half the 10 s Store query timeout leaves room for a loaded CI host; the
	// per-event plan this replaces needed most of the timeout at this volume.
	require.Less(t, plan.ExecutionTime, float64((5 * time.Second).Milliseconds()), "preview count execution: %s", encoded)

	tx, err := s.GetMaster().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	counts, _, err := countRetentionExpiry(ctx, tx, policy, at)
	require.NoError(t, err, "preview count within the Store query timeout")
	require.GreaterOrEqual(t, counts.Total, int64(auditEvents))
}
