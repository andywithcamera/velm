package db

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// The save response reports its version from the post-write snapshot. If that
// snapshot does not carry the version columns, the header is silently never
// set and the phantom "updates are available" banner survives. This asserts
// against a real row, because the snapshot is a projection and adding or
// dropping a system column there is invisible from the call site.
func TestSaveSnapshotCarriesRecordVersion(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}
	t.Setenv("DATABASE_URL", url)
	os.Setenv("DATABASE_URL", url)
	if err := ConnectToDB(); err != nil {
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(CloseDB)

	ctx := context.Background()
	var (
		id        string
		count     int64
		updatedAt time.Time
	)
	err := Pool.QueryRow(ctx, `SELECT "_id"::text, "_update_count", "_updated_at" FROM _docs_article ORDER BY "_id" LIMIT 1`).
		Scan(&id, &count, &updatedAt)
	if err != nil {
		t.Fatalf("read a _docs_article row: %v", err)
	}

	snapshot := loadRecordSnapshotWithQuerier(ctx, Pool, "_docs_article", id)
	if len(snapshot) == 0 {
		t.Fatalf("snapshot for _docs_article %s is empty", id)
	}
	if _, ok := snapshot["_update_count"]; !ok {
		t.Errorf("snapshot has no _update_count; recorded keys: %v", keysOf(snapshot))
	}
	if _, ok := snapshot["_updated_at"]; !ok {
		t.Errorf("snapshot has no _updated_at; recorded keys: %v", keysOf(snapshot))
	}

	got := RecordVersionValue(snapshot)
	if got != strconv.FormatInt(count, 10) {
		t.Errorf("RecordVersionValue(snapshot) = %q, want the stored counter %d", got, count)
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
