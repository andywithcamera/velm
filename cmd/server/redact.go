package main

import "regexp"

// redactSecrets strips credential material from error text so it is safe to
// log: URL userinfo (postgres://user:pass@host) and DSN password key/values.
var (
	dsnURLUserInfo = regexp.MustCompile(`(?i)([a-z0-9+.\-]+://)[^:@/\s]+:([^@/\s]+)@`)
	dsnPasswordKv  = regexp.MustCompile(`(?i)(password\s*=\s*)[^\s]*`)
)

func redactSecrets(s string) string {
	s = dsnURLUserInfo.ReplaceAllString(s, `${1}***:***@`)
	s = dsnPasswordKv.ReplaceAllString(s, `${1}***`)
	return s
}
