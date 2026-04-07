package db

import "testing"

func TestParseRecordIDValueUUID(t *testing.T) {
	id, err := ParseRecordIDValue("_table", "123e4567-e89b-12d3-a456-426614174000")
	if err != nil {
		t.Fatalf("ParseRecordIDValue returned error: %v", err)
	}

	value, ok := id.(string)
	if !ok {
		t.Fatalf("expected string id, got %T", id)
	}
	if value != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("unexpected id value %q", value)
	}
}
