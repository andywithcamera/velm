package utils

import "regexp"

var canonicalUUIDPattern = regexp.MustCompile("^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$")

// Helper function to validate if a string is a valid UUID
func IsValidUUID(u string) bool {
	return canonicalUUIDPattern.MatchString(u)
}
