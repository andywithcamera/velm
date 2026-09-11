package main

import "testing"

func TestFingerprintKey(t *testing.T) {
	if got := fingerprintKey("sk-proj-0123456789abcdefghij"); len(got) > 14 || got == "sk-proj-0123456789abcdefghij" {
		t.Fatalf("fingerprint leaks key: %q", got)
	}
}
