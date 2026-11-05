package security

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Provider destinations are attacker-controlled in one specific sense: an
// admin can write _ai_provider.base_url, and the server then attaches a
// decrypted provider API key to a request for that URL. Left unconstrained
// that is a credential-theft primitive against the platform's own secret
// store -- point base_url at a host you control, sync an existing connection,
// read the key out of the request. It also reaches cloud metadata endpoints
// from inside the network.
//
// So destinations are default-deny: HTTPS only, and only to publicly
// routable addresses. Self-hosters who deliberately talk to a model server on
// loopback or a private network opt in with VELM_ALLOW_PRIVATE_PROVIDER_URLS.
//
// Validation happens at two points, and both matter:
//
//  1. before the key is attached (validateProviderURL), so a rejected
//     destination never sees a request carrying a secret at all;
//  2. inside the dialer, on the address actually being connected to, which
//     is what closes the DNS-rebinding gap -- checking the hostname string
//     alone lets a name resolve to a public address during validation and a
//     private one at connect time.

// ErrProviderDestination means the configured provider URL was refused before
// any request was made. Callers may show it to an admin: it names no internal
// detail, only the rule that was broken.
var ErrProviderDestination = errors.New("provider destination not allowed")

// ErrProviderRequest means the request was made but failed. It deliberately
// carries no transport detail, which would otherwise let an admin use the
// error message as a blind port scanner (dial timeouts, TLS errors and DNS
// failures are all distinguishable).
var ErrProviderRequest = errors.New("provider request failed")

// providerLookupIP is a seam so tests can resolve names without DNS.
var providerLookupIP = net.DefaultResolver.LookupIPAddr

// allowPrivateProviderDestinations reports whether this deployment has opted
// in to internal model servers (local Ollama, a LAN inference box).
func allowPrivateProviderDestinations() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VELM_ALLOW_PRIVATE_PROVIDER_URLS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// isBlockedProviderIP reports whether an address is outside the public
// internet. Every class here is reachable from the server and not from an
// arbitrary remote party, which is exactly the property SSRF exploits.
func isBlockedProviderIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Normalise IPv4-mapped IPv6 (::ffff:127.0.0.1) to its 4-byte form so the
	// v4 ranges below actually match.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Ranges Go's helpers do not classify.
	for _, cidr := range blockedProviderCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

var blockedProviderCIDRs = func() []*net.IPNet {
	// Carrier-grade NAT, benchmarking, and the various "this network"
	// reservations. Not routable from the internet, so not a provider.
	const list = "100.64.0.0/10,192.0.0.0/24,192.0.2.0/24,198.18.0.0/15,198.51.100.0/24,203.0.113.0/24,240.0.0.0/4,64:ff9b:1::/48,2001:db8::/32,2002::/16"
	out := make([]*net.IPNet, 0, 12)
	for _, part := range strings.Split(list, ",") {
		if _, n, err := net.ParseCIDR(strings.TrimSpace(part)); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// resolveAllowedProviderIPs resolves a host and, unless the deployment has
// opted in to internal destinations, refuses the whole name if any address it
// answers with is non-public.
//
// Both the pre-flight check and the dialer call this, and that is the point:
// the dialer resolves again at connect time rather than trusting the earlier
// answer, so a name that resolves publicly during validation and privately a
// moment later still cannot be reached.
func resolveAllowedProviderIPs(ctx context.Context, host string, allowPrivate bool) ([]net.IPAddr, error) {
	var ips []net.IPAddr
	if literal := net.ParseIP(host); literal != nil {
		ips = []net.IPAddr{{IP: literal}}
	} else {
		var err error
		ips, err = providerLookupIP(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("%w: host could not be resolved", ErrProviderDestination)
		}
	}
	if allowPrivate {
		return ips, nil
	}
	for _, addr := range ips {
		if isBlockedProviderIP(addr.IP) {
			return nil, fmt.Errorf("%w: host resolves to a non-public address", ErrProviderDestination)
		}
	}
	return ips, nil
}

// validateProviderURL parses and checks a provider base URL. It resolves the
// host too, so a name pointing only at private addresses is refused here --
// before the caller attaches a key.
func validateProviderURL(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: malformed URL", ErrProviderDestination)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in URL are not supported", ErrProviderDestination)
	}

	allowPrivate := allowPrivateProviderDestinations()
	switch strings.ToLower(u.Scheme) {
	case "https":
		// Always acceptable for public destinations; private addresses are
		// gated below.
	case "http":
		// Plain HTTP only makes sense to something on the operator's own
		// network, and only when they have said so.
		if !allowPrivate {
			return nil, fmt.Errorf("%w: http is not allowed, use https", ErrProviderDestination)
		}
	default:
		return nil, fmt.Errorf("%w: unsupported scheme %q", ErrProviderDestination, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrProviderDestination)
	}

	// Resolved now, so a name that points somewhere private is refused before
	// a key is anywhere near the request. "Does not resolve" and "resolves to
	// a blocked address" give the same answer on purpose: the difference is
	// only useful to someone mapping the network.
	if _, err := resolveAllowedProviderIPs(ctx, host, allowPrivate); err != nil {
		return nil, err
	}
	return u, nil
}

// safeProviderTransport builds a transport that refuses to connect anywhere
// non-public, checking the address it is about to dial rather than the name
// it was given.
func safeProviderTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	allowPrivate := allowPrivateProviderDestinations()

	return &http.Transport{
		Proxy: nil, // an operator's proxy could route this anywhere
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("%w: bad address", ErrProviderRequest)
			}
			// Resolved again, here, and connected to by address: checking
			// the name earlier is not enough (DNS rebinding).
			ips, err := resolveAllowedProviderIPs(ctx, host, allowPrivate)
			if err != nil {
				return nil, err
			}
			// Connect to the address we validated, not to the name again.
			var lastErr error
			for _, ip := range ips {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, fmt.Errorf("%w: %v", ErrProviderRequest, lastErr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
}

// refuseProviderRedirect is the redirect policy for every provider call.
// Following a redirect would send the key to a host that was never validated,
// so a 3xx is a terminal response, not a hop.
func refuseProviderRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// newSafeProviderHTTPClient is the client providers are called with.
func newSafeProviderHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		Transport:     safeProviderTransport(),
		CheckRedirect: refuseProviderRedirect,
	}
}

// logProviderFailure records the detail that the caller must not see. Never
// called with a key or a header value.
func logProviderFailure(host string, err error) {
	log.Printf("level=warn event=provider_request_failed host=%q err=%v", host, err)
}

// ValidateProviderDestination reports whether raw is an acceptable place to
// send a provider key, using exactly the checks a live call would apply. It
// exists so a destination can be refused at the point a key is entered,
// rather than only when a sync is attempted.
func ValidateProviderDestination(ctx context.Context, raw string) error {
	_, err := validateProviderURL(ctx, raw)
	return err
}
