package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

// stubProviderLookup replaces DNS for the duration of a test. Passing no
// addresses makes the host resolve to a single public address, which is what
// the existing list-models tests need.
func stubProviderLookup(t *testing.T, ips []string) {
	t.Helper()
	if len(ips) == 0 {
		ips = []string{"93.184.216.34"}
	}
	addrs := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		addrs = append(addrs, net.IPAddr{IP: ip})
	}
	prev := providerLookupIP
	providerLookupIP = func(context.Context, string) ([]net.IPAddr, error) { return addrs, nil }
	t.Cleanup(func() { providerLookupIP = prev })
}

// lookupSequence returns a different answer on each call, which is how a
// rebinding attack looks from the resolver.
func lookupSequence(t *testing.T, answers ...string) *int {
	t.Helper()
	calls := 0
	prev := providerLookupIP
	providerLookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		idx := calls
		if idx >= len(answers) {
			idx = len(answers) - 1
		}
		calls++
		return []net.IPAddr{{IP: net.ParseIP(answers[idx])}}, nil
	}
	t.Cleanup(func() { providerLookupIP = prev })
	return &calls
}

// emptyTransport fails the test if anything tries to send a request.
func emptyTransport(t *testing.T) roundTripperFunc {
	return func(r *http.Request) (*http.Response, error) {
		t.Fatalf("request reached the network: %s %s", r.Method, r.URL)
		return nil, nil
	}
}

func TestProviderRejectsHTTPBaseURLBeforeAnyRequest(t *testing.T) {
	stubProviderLookup(t, nil)
	pc := &ProviderClient{client: &http.Client{Transport: emptyTransport(t)}}

	_, err := pc.ListModels("http://api.example.com", "/v1/models", "secret-key", nil)
	if !errors.Is(err, ErrProviderDestination) {
		t.Fatalf("err = %v, want ErrProviderDestination", err)
	}
}

func TestProviderRejectsBlockedDestinations(t *testing.T) {
	cases := []struct{ name, host string }{
		{"ipv4 loopback", "127.0.0.1"},
		{"ipv4 loopback alt", "127.1.2.3"},
		{"private class a", "10.0.0.5"},
		{"private class b", "172.16.3.4"},
		{"private class c", "192.168.1.9"},
		{"link-local metadata", "169.254.169.254"},
		{"carrier-grade nat", "100.64.1.1"},
		{"benchmarking", "198.18.0.1"},
		{"unspecified v4", "0.0.0.0"},
		{"multicast", "224.0.0.1"},
		{"ipv6 loopback", "::1"},
		{"ipv6 ula", "fc00::1"},
		{"ipv6 link-local", "fe80::1"},
		{"ipv6 unspecified", "::"},
		{"ipv4-mapped loopback", "::ffff:127.0.0.1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubProviderLookup(t, nil)
			pc := &ProviderClient{client: &http.Client{Transport: emptyTransport(t)}}

			raw := "https://[" + tc.host + "]/v1/models"
			if net.ParseIP(tc.host).To4() != nil {
				raw = "https://" + tc.host + "/v1/models"
			}
			if _, err := pc.ListModels(raw, "", "secret-key", nil); !errors.Is(err, ErrProviderDestination) {
				t.Fatalf("%s: err = %v, want ErrProviderDestination", tc.host, err)
			}
		})
	}
}

func TestProviderRejectsNameResolvingToBlockedAddress(t *testing.T) {
	stubProviderLookup(t, []string{"93.184.216.34", "10.1.2.3"})
	pc := &ProviderClient{client: &http.Client{Transport: emptyTransport(t)}}

	_, err := pc.ListModels("https://internal.example.com", "/v1/models", "secret-key", nil)
	if !errors.Is(err, ErrProviderDestination) {
		t.Fatalf("err = %v, want ErrProviderDestination when any address is private", err)
	}
}

// The rebinding case: the name answers with a public address during the
// pre-flight check and a private one at connect time. Validation alone cannot
// catch this; the dialer resolving again and connecting by address does.
func TestProviderDialerRefusesRebinding(t *testing.T) {
	ctx := context.Background()
	lookupSequence(t, "93.184.216.34", "127.0.0.1")

	if _, err := validateProviderURL(ctx, "https://rebind.example.com"); err != nil {
		t.Fatalf("pre-flight should pass on the public answer: %v", err)
	}

	conn, err := safeProviderTransport().DialContext(ctx, "tcp", "rebind.example.com:443")
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, ErrProviderDestination) {
		t.Fatalf("dial err = %v, want ErrProviderDestination", err)
	}
}

func TestProviderDialerBlocksLoopbackLiteral(t *testing.T) {
	conn, err := safeProviderTransport().DialContext(context.Background(), "tcp", "127.0.0.1:443")
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, ErrProviderDestination) {
		t.Fatalf("dial err = %v, want ErrProviderDestination", err)
	}
}

func TestProviderAllowsPublicHTTPS(t *testing.T) {
	stubProviderLookup(t, nil)
	var gotKey string
	pc := &ProviderClient{client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotKey = r.Header.Get("x-api-key")
		if r.URL.String() != "https://api.openai.com/v1/models" {
			t.Errorf("url = %s", r.URL)
		}
		return fakeResponse(200, `{"data":[{"id":"gpt-5"}]}`), nil
	})}}

	models, err := pc.ListModels("https://api.openai.com", "/v1/models", "secret-key", nil)
	if err != nil {
		t.Fatalf("public https should be allowed: %v", err)
	}
	if gotKey != "secret-key" || len(models) != 1 {
		t.Fatalf("key/len = %q/%d", gotKey, len(models))
	}
}

func TestProviderDoesNotFollowRedirect(t *testing.T) {
	stubProviderLookup(t, nil)
	calls := 0
	pc := &ProviderClient{client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := fakeResponse(302, "")
		resp.Header.Set("Location", "https://attacker.example/collect")
		return resp, nil
	})}}

	_, err := pc.ListModels("https://api.example.com", "/v1/models", "secret-key", nil)
	if err == nil {
		t.Fatal("a redirect must fail, not be followed")
	}
	if !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("err = %v, want redirect refused", err)
	}
	if calls != 1 {
		t.Fatalf("transport calls = %d, want 1 (the key must not be re-sent)", calls)
	}

	// The client itself refuses to follow, independently of the transport.
	client := newSafeProviderHTTPClient()
	req, _ := http.NewRequest(http.MethodGet, "https://attacker.example/collect", nil)
	if err := client.CheckRedirect(req, []*http.Request{req}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect = %v, want ErrUseLastResponse", err)
	}
}

func TestProviderErrorsDoNotLeakDestination(t *testing.T) {
	stubProviderLookup(t, nil)
	pc := &ProviderClient{client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp 10.9.9.9:443: connect: connection refused")
	})}}

	_, err := pc.ListModels("https://api.example.com", "/v1/models", "secret-key", nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, ErrProviderRequest) {
		t.Fatalf("err = %v, want ErrProviderRequest", err)
	}
	for _, leak := range []string{"10.9.9.9", "dial tcp", "connection refused", "api.example.com"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaks %q: %v", leak, err)
		}
	}
}

func TestProviderRefusesPathThatChangesHost(t *testing.T) {
	stubProviderLookup(t, nil)
	pc := &ProviderClient{client: &http.Client{Transport: emptyTransport(t)}}

	for _, path := range []string{"https://attacker.example/x", "//attacker.example/x", "ftp://attacker.example/x"} {
		_, err := pc.ListModels("https://api.example.com", path, "secret-key", nil)
		if !errors.Is(err, ErrProviderDestination) {
			t.Fatalf("path %q: err = %v, want ErrProviderDestination", path, err)
		}
	}
}

func TestProviderPrivateDestinationsRequireOptIn(t *testing.T) {
	stubProviderLookup(t, nil)
	pc := &ProviderClient{client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(200, `{"data":[{"id":"llama3"}]}`), nil
	})}}

	// Off by default: a local model server is refused.
	if _, err := pc.ListModels("http://127.0.0.1:11434", "/api/tags", "k", nil); !errors.Is(err, ErrProviderDestination) {
		t.Fatalf("default err = %v, want ErrProviderDestination", err)
	}

	// Opted in: the self-hoster's own inference box is reachable.
	t.Setenv("VELM_ALLOW_PRIVATE_PROVIDER_URLS", "true")
	if _, err := pc.ListModels("http://127.0.0.1:11434", "/api/tags", "k", nil); err != nil {
		t.Fatalf("opted-in err = %v, want success", err)
	}

	// And the opt-in still refuses a scheme that is not http or https.
	if _, err := pc.ListModels("file:///etc/passwd", "/", "k", nil); !errors.Is(err, ErrProviderDestination) {
		t.Fatalf("file scheme err = %v, want ErrProviderDestination", err)
	}
}
