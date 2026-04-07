package utils

import "testing"

func TestIsValidUUID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "valid v4", input: "e2402d0b-f30a-49b3-bc6c-5c8982fe6cc5", want: true},
		{name: "valid canonical non-rfc seed uuid", input: "90000000-0000-0000-0000-000000000101", want: true},
		{name: "invalid chars", input: "zzzzzzzz-f30a-49b3-bc6c-5c8982fe6cc5", want: false},
		{name: "missing dashes", input: "e2402d0bf30a49b3bc6c5c8982fe6cc5", want: false},
		{name: "valid canonical version 6", input: "e2402d0b-f30a-69b3-bc6c-5c8982fe6cc5", want: true},
		{name: "valid canonical variant 7", input: "e2402d0b-f30a-49b3-7c6c-5c8982fe6cc5", want: true},
		{name: "too short", input: "e2402d0b-f30a-49b3-bc6c-5c8982fe6cc", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsValidUUID(tt.input)
			if got != tt.want {
				t.Fatalf("IsValidUUID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
