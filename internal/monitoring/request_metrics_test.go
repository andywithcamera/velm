package monitoring

import (
	"strings"
	"testing"
	"time"
)

func TestRequestMetricsSnapshotTracksSlowestQuery(t *testing.T) {
	t.Parallel()

	metrics := NewRequestMetrics("req-123", time.Unix(1700000000, 0))
	metrics.RecordDBQuery(5*time.Millisecond, "SELECT 1")
	metrics.RecordDBQuery(17*time.Millisecond, "  SELECT   *   FROM  _user WHERE  _id = $1 ")

	snapshot := metrics.Snapshot()
	if snapshot.RequestID != "req-123" {
		t.Fatalf("snapshot.RequestID = %q, want %q", snapshot.RequestID, "req-123")
	}
	if snapshot.DBQueryCount != 2 {
		t.Fatalf("snapshot.DBQueryCount = %d, want %d", snapshot.DBQueryCount, 2)
	}
	if snapshot.DBDuration != 22*time.Millisecond {
		t.Fatalf("snapshot.DBDuration = %s, want %s", snapshot.DBDuration, 22*time.Millisecond)
	}
	if snapshot.DBSlowest != 17*time.Millisecond {
		t.Fatalf("snapshot.DBSlowest = %s, want %s", snapshot.DBSlowest, 17*time.Millisecond)
	}
	if snapshot.DBSlowestQuery != "SELECT * FROM _user WHERE _id = $1" {
		t.Fatalf("snapshot.DBSlowestQuery = %q", snapshot.DBSlowestQuery)
	}
}

func TestNormalizeSQLTrimsAndTruncates(t *testing.T) {
	t.Parallel()

	if got := normalizeSQL("  SELECT   1  "); got != "SELECT 1" {
		t.Fatalf("normalizeSQL() = %q, want %q", got, "SELECT 1")
	}

	longSQL := "SELECT " + strings.Repeat("x", 260)
	got := normalizeSQL(longSQL)
	if len(got) > 240 {
		t.Fatalf("normalizeSQL() length = %d, want <= 240", len(got))
	}
}
