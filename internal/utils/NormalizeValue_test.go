package utils

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNormalizeValueHandlesPGTypeUUID(t *testing.T) {
	uuid := pgtype.UUID{
		Bytes: [16]byte{0xe2, 0x40, 0x2d, 0x0b, 0xf3, 0x0a, 0x49, 0xb3, 0xbc, 0x6c, 0x5c, 0x89, 0x82, 0xfe, 0x6c, 0xc5},
		Valid: true,
	}

	got := NormalizeValue(uuid)
	want := "e2402d0b-f30a-49b3-bc6c-5c8982fe6cc5"
	if got != want {
		t.Fatalf("NormalizeValue(pgtype.UUID) = %#v, want %q", got, want)
	}
}

func TestNormalizeValueHandlesNilPGTypeUUID(t *testing.T) {
	got := NormalizeValue(pgtype.UUID{})
	if got != nil {
		t.Fatalf("NormalizeValue(invalid pgtype.UUID) = %#v, want nil", got)
	}
}
