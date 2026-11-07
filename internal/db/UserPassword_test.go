package db

import "testing"

func TestIsBcryptHash(t *testing.T) {
	hash, err := HashUserPassword("correct horse battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"generated hash", hash, true},
		{"surrounding space", "  " + hash + "\n", true},
		{"empty", "", false},
		{"plaintext", "hunter2", false},
		{"wrong length", hash[:len(hash)-1], false},
		{"not base64", "$2a$10$" + "!" + hash[8:], false},
		{"cost too low", "$2a$03$" + hash[7:], false},
		{"cost too high", "$2a$32$" + hash[7:], false},
		{"unknown version", "$2z$10$" + hash[7:], false},
	}
	for _, tc := range cases {
		if got := IsBcryptHash(tc.value); got != tc.want {
			t.Errorf("%s: IsBcryptHash(%q) = %v, want %v", tc.name, tc.value, got, tc.want)
		}
	}
}

func TestHashUserPasswordRejectsWeakInput(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{"empty", ""},
		{"whitespace only", "          "},
		{"too short", "shortpass"},
		{"over bcrypt window", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	for _, tc := range cases {
		if _, err := HashUserPassword(tc.password); err == nil {
			t.Errorf("%s: expected an error for %q", tc.name, tc.password)
		}
	}
}

func TestIsDirectlyWritableColumnBlocksPasswordHash(t *testing.T) {
	if IsDirectlyWritableColumn(UserTableName, UserPasswordColumn) {
		t.Fatalf("%s.%s must not be writable from the client", UserTableName, UserPasswordColumn)
	}
	if IsDirectlyWritableColumn("_USER", "Password_Hash") {
		t.Fatalf("table and column matching must be case-insensitive")
	}
	if !IsDirectlyWritableColumn(UserTableName, "email") {
		t.Fatalf("ordinary _user columns must stay writable")
	}
	if !IsDirectlyWritableColumn("_task", UserPasswordColumn) {
		t.Fatalf("the guard must only apply to the _user table")
	}
}

func TestIsUserPasswordField(t *testing.T) {
	for _, field := range []string{"password", "Password", " PASSWORD "} {
		if !IsUserPasswordField(field) {
			t.Errorf("%q should be recognised as the synthetic password field", field)
		}
	}
	for _, field := range []string{"password_hash", "pass", ""} {
		if IsUserPasswordField(field) {
			t.Errorf("%q must not be treated as the synthetic password field", field)
		}
	}
}
