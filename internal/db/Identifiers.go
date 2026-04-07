package db

import (
	"fmt"
	"regexp"
	"strings"
)

var identifierRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func IsSafeIdentifier(name string) bool {
	return identifierRegex.MatchString(name)
}

func QuoteIdentifier(name string) (string, error) {
	if !IsSafeIdentifier(name) {
		return "", fmt.Errorf("unsafe SQL identifier: %q", name)
	}
	return `"` + name + `"`, nil
}

func QuoteIdentifierList(names []string) ([]string, error) {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		q, err := QuoteIdentifier(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		quoted = append(quoted, q)
	}
	return quoted, nil
}
