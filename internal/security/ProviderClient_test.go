package security

import (
	"io"
	"strings"

	"net/http"
	"testing"
)

// roundTripperFunc stubs the network so tests don't need a real listener
// (the dev VM has no IPv6 loopback, which breaks httptest.NewServer).
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestListModelsOpenAI(t *testing.T) {
	var gotAuth string
	pc := NewProviderClient()
	pc.HTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		return fakeResponse(200, `{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"}]}`), nil
	})}
	models, err := pc.ListModels("https://api.example.com", "/v1/models", "test-key", map[string]any{"auth_header": "Authorization"})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if len(models) != 2 || models[0].ModelID != "gpt-5" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestListModelsAnthropic(t *testing.T) {
	var gotKey, gotVersion string
	pc := NewProviderClient()
	pc.HTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		return fakeResponse(200, `{"data":[{"id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5"}]}`), nil
	})}
	models, err := pc.ListModels("https://api.example.com", "/v1/models", "test-key",
		map[string]any{"auth_header": "x-api-key", "version_header": "anthropic-version", "version_value": "2023-06-01"})
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "test-key" || gotVersion != "2023-06-01" {
		t.Errorf("auth/version headers wrong: %q %q", gotKey, gotVersion)
	}
	if len(models) != 1 || models[0].ModelID != "claude-sonnet-4-5" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestListModelsHTTPError(t *testing.T) {
	pc := NewProviderClient()
	pc.HTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return fakeResponse(401, `{"error":{"message":"bad key"}}`), nil
	})}
	if _, err := pc.ListModels("https://api.example.com", "/v1/models", "bad", nil); err == nil {
		t.Fatal("want error on non-200")
	}

}
