package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	// UserTableName is the system users table.
	UserTableName = "_user"

	// UserPasswordField is the form-only field that sets a user's password.
	// It is never a column and never round-trips: the submitted value is
	// bcrypt-hashed during save and written to UserPasswordColumn.
	UserPasswordField = "password"

	// UserPasswordColumn holds the hashed credential on _user. It is not
	// directly writable, so a stray value can never be stored as-is.
	UserPasswordColumn = "password_hash"

	// MinUserPasswordLength is the shortest password the form accepts.
	MinUserPasswordLength = 10

	// MaxUserPasswordBytes is bcrypt's input window. bcrypt hashes at most
	// 72 bytes and silently ignores anything past it, so a longer password
	// would be accepted at sign-up and then only partly checked at login.
	MaxUserPasswordBytes = 72
)

// bcryptHashPattern matches every hash shape x/crypto/bcrypt will compare:
// $2a$, $2b$, $2y$ (and the historic $2x$), a two-digit cost, then 53
// characters of the base64 variant bcrypt uses. Length is always 60.
var bcryptHashPattern = regexp.MustCompile(`^\$2[abxy]\$([0-9]{2})\$[./A-Za-z0-9]{53}$`)

// IsBcryptHash reports whether value is a bcrypt hash this build can verify.
func IsBcryptHash(value string) bool {
	match := bcryptHashPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return false
	}
	cost := 0
	for _, digit := range match[1] {
		cost = cost*10 + int(digit-'0')
	}
	return cost >= bcrypt.MinCost && cost <= bcrypt.MaxCost
}

// ValidateUserPassword applies the rules for a password typed by a human.
func ValidateUserPassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password is required")
	}
	if utf8.RuneCountInString(password) < MinUserPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinUserPasswordLength)
	}
	if len(password) > MaxUserPasswordBytes {
		return fmt.Errorf("password must be at most %d bytes (bcrypt ignores anything longer)", MaxUserPasswordBytes)
	}
	return nil
}

// HashUserPassword validates and hashes a password for storage.
func HashUserPassword(password string) (string, error) {
	if err := ValidateUserPassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// IsUserPasswordField reports whether field is the synthetic password field
// rather than a real column.
func IsUserPasswordField(field string) bool {
	return strings.TrimSpace(strings.ToLower(field)) == UserPasswordField
}

// IsDirectlyWritableColumn reports whether columnName may be written with a
// value supplied by the client. A user's password hash is not: it is only ever
// produced by hashing UserPasswordField.
func IsDirectlyWritableColumn(tableName, columnName string) bool {
	if strings.EqualFold(strings.TrimSpace(tableName), UserTableName) &&
		strings.EqualFold(strings.TrimSpace(columnName), UserPasswordColumn) {
		return false
	}
	return true
}

// UserPasswordState describes whether a user can actually log in with a
// password.
type UserPasswordState string

const (
	// UserPasswordStateOK means a bcrypt hash is stored.
	UserPasswordStateOK UserPasswordState = "ok"
	// UserPasswordStateMissing means no hash is stored at all.
	UserPasswordStateMissing UserPasswordState = "missing"
	// UserPasswordStateUnusable means a value is stored but it is not a
	// bcrypt hash, so no password can ever match it.
	UserPasswordStateUnusable UserPasswordState = "unusable"
	// UserPasswordStateUnknown means the record could not be read.
	UserPasswordStateUnknown UserPasswordState = "unknown"
)

// LoadUserPasswordState reports whether the given user has a usable password.
func LoadUserPasswordState(ctx context.Context, userID string) UserPasswordState {
	userID = strings.TrimSpace(userID)
	if userID == "" || Pool == nil {
		return UserPasswordStateUnknown
	}
	var stored string
	err := Pool.QueryRow(ctx, `
		SELECT COALESCE(password_hash, '')
		FROM _user
		WHERE _id::text = $1
	`, userID).Scan(&stored)
	if err != nil {
		return UserPasswordStateUnknown
	}
	switch {
	case strings.TrimSpace(stored) == "":
		return UserPasswordStateMissing
	case IsBcryptHash(stored):
		return UserPasswordStateOK
	default:
		return UserPasswordStateUnusable
	}
}

// UserPasswordFormColumn is the synthetic, form-only column that renders the
// password input on a _user record. It is deliberately not nullable-required:
// leaving it blank keeps the stored password untouched.
func UserPasswordFormColumn() Column {
	return Column{
		NAME:        UserPasswordField,
		LABEL:       "Password",
		DATA_TYPE:   UserPasswordField,
		IS_NULLABLE: true,
	}
}
