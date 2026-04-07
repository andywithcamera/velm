package db

import "testing"

func TestNormalizeBootstrapUserInputNormalizesEmailAndDefaultsName(t *testing.T) {
	input, err := normalizeBootstrapUserInput(BootstrapUserInput{
		Email:    "  ADMIN.USER@example.com ",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("normalizeBootstrapUserInput() error = %v", err)
	}

	if input.Email != "admin.user@example.com" {
		t.Fatalf("normalized email = %q, want %q", input.Email, "admin.user@example.com")
	}
	if input.Name != "Admin User" {
		t.Fatalf("default name = %q, want %q", input.Name, "Admin User")
	}
}

func TestNormalizeBootstrapUserInputRequiresPassword(t *testing.T) {
	_, err := normalizeBootstrapUserInput(BootstrapUserInput{
		Email: "admin@example.com",
	})
	if err == nil {
		t.Fatal("expected password validation error")
	}
}

func TestDefaultBootstrapUserNameFallsBackToEmail(t *testing.T) {
	got := defaultBootstrapUserName("@")
	if got != "@" {
		t.Fatalf("defaultBootstrapUserName() = %q, want %q", got, "@")
	}
}
