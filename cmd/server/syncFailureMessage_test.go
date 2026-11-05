package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"velm/internal/security"
)

// The sync endpoint returns its error to an admin. That admin can also edit
// base_url, so anything the message reveals about the network is a probe
// channel: a timeout proves a host is filtered, a refusal proves it is up.
func TestSyncFailureMessageHidesTransportDetail(t *testing.T) {
	internal := []error{
		fmt.Errorf("list models: %w", fmt.Errorf("%w: dial tcp 10.9.9.9:443: connection refused", security.ErrProviderRequest)),
		fmt.Errorf("list models: %w", fmt.Errorf("%w: dial tcp 169.254.169.254:80: i/o timeout", security.ErrProviderRequest)),
		fmt.Errorf("list models: %w", fmt.Errorf("%w: tls: failed to verify certificate: x509: certificate signed by unknown authority", security.ErrProviderRequest)),
		fmt.Errorf("list models: %w", fmt.Errorf("%w: host could not be resolved", security.ErrProviderRequest)),
	}

	for _, err := range internal {
		msg := syncFailureMessage(err)
		for _, leak := range []string{"10.9.9.9", "169.254.169.254", "dial tcp", "timeout", "tls", "x509", "resolved"} {
			if strings.Contains(msg, leak) {
				t.Fatalf("message %q leaks %q", msg, leak)
			}
		}
	}
}

func TestSyncFailureMessageNamesRefusedDestination(t *testing.T) {
	err := fmt.Errorf("list models: %w: http is not allowed, use https", security.ErrProviderDestination)
	msg := syncFailureMessage(err)
	if !strings.Contains(msg, "refused") || !strings.Contains(msg, "use https") {
		t.Fatalf("message %q should tell the admin what to fix", msg)
	}
}

func TestSyncFailureMessageDefaultsToGeneric(t *testing.T) {
	msg := syncFailureMessage(errors.New("decrypt key: cipher: message authentication failed"))
	if strings.Contains(msg, "decrypt") || strings.Contains(msg, "cipher") {
		t.Fatalf("message %q leaks internal detail", msg)
	}
}
