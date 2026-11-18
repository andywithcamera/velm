package db

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRetryDebugTrace(t *testing.T) {
	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-debug", map[string]any{"attempts": 2}, []string{"A"}, nil)
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)
	dump := func(tag string) {
		rows, err := Pool.Query(ctx, `SELECT _id::text, COALESCE(work_type,''), state, COALESCE(closure_reason,''), COALESCE(variables->'block'->>'type',''), COALESCE(variables->>'attempt_no','-') FROM base_task WHERE _id=$1 OR parent_task_id=$1 OR parent_task_id IN (SELECT _id FROM base_task WHERE parent_task_id=$1) ORDER BY _created_at`, rootID)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		t.Logf("--- %s", tag)
		for rows.Next() {
			var id, wt, st, r, bt, att string
			_ = rows.Scan(&id, &wt, &st, &r, &bt, &att)
			v := taskVars(ctx, t, id)
			b, _ := json.Marshal(v["outputs"])
			t.Logf("  %s wt=%s state=%s reason=%s block=%s attempt=%s outputs=%s", id[:8], wt, st, r, bt, att, b)
		}
		var vars []byte
		_ = Pool.QueryRow(ctx, `SELECT variables FROM base_task WHERE _id=$1`, retryID).Scan(&vars)
		t.Logf("  retry vars: %s", string(vars))
	}
	dump("spawned")
	for i := 0; i < 2; i++ {
		ch := retryChildren(t, ctx, retryID)
		if len(ch) == 0 { t.Fatal("no children") }
		var newest *retryChild
		for j := range ch { if newest == nil || ch[j].Attempt > newest.Attempt { newest = &ch[j] } }
		t.Logf("failing %s attempt %d", newest.ID[:8], newest.Attempt)
		if err := CloseTaskWithReason(ctx, newest.ID, "", "failed"); err != nil { t.Fatalf("close: %v", err) }
		dump("after failure")
	}
}
