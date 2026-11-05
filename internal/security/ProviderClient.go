package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ProviderClient performs authenticated read-only calls against a provider's
// public API (initially: list models). It is deliberately narrow: it never
// returns raw key material and never logs headers.
//
// The HTTP client it uses is not a default one. See
// newSafeProviderHTTPClient: destinations are validated, redirects are not
// followed, and the key travels only to an address that was checked.
type ProviderClient struct {
	client *http.Client
}

func NewProviderClient() *ProviderClient {
	return &ProviderClient{client: newSafeProviderHTTPClient()}
}

// ProviderModel is one entry from a provider's list-models response.
type ProviderModel struct {
	ModelID     string         `json:"model_id"`
	DisplayName string         `json:"display_name"`
	Metadata    map[string]any `json:"metadata"`
}

// providerRequestURL combines a provider's base URL with its list-models
// path. The path is treated as a path: a value that carries a scheme or host
// is rejected rather than concatenated, so provider_metadata cannot redirect
// the request to a host the base URL did not already name.
func providerRequestURL(base *url.URL, listPath string) (*url.URL, error) {
	rel, err := url.Parse(strings.TrimSpace(listPath))
	if err != nil {
		return nil, fmt.Errorf("%w: malformed list path", ErrProviderDestination)
	}
	if rel.Scheme != "" || rel.Host != "" {
		return nil, fmt.Errorf("%w: list path must be a path, not a URL", ErrProviderDestination)
	}

	out := *base
	out.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(rel.Path, "/")
	out.RawQuery = rel.RawQuery
	out.Fragment = ""
	return &out, nil
}

// ListModels calls the provider's model-list endpoint with the given
// plaintext key and returns the parsed catalog. Wire specifics (auth header,
// required version headers) come from _ai_provider.provider_metadata.
func (pc *ProviderClient) ListModels(baseURL, listPath, plaintextKey string, meta map[string]any) ([]ProviderModel, error) {
	return pc.ListModelsContext(context.Background(), baseURL, listPath, plaintextKey, meta)
}

// ListModelsContext is ListModels with a caller-supplied context, used when
// the call is made on behalf of an in-flight HTTP request.
func (pc *ProviderClient) ListModelsContext(ctx context.Context, baseURL, listPath, plaintextKey string, meta map[string]any) ([]ProviderModel, error) {
	base, err := validateProviderURL(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	target, err := providerRequestURL(base, listPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: could not build request", ErrProviderRequest)
	}

	// Reached only once the destination has been validated; a refused host
	// never sees a request, so never sees the key.
	authHeader, _ := meta["auth_header"].(string)
	switch authHeader {
	case "Authorization":
		req.Header.Set("Authorization", "Bearer "+plaintextKey)
	default:
		if authHeader == "" {
			authHeader = "x-api-key"
		}
		req.Header.Set(authHeader, plaintextKey)
	}
	if v, _ := meta["version_header"].(string); v != "" {
		req.Header.Set(v, str(meta["version_value"]))
	}
	req.Header.Set("Accept", "application/json")

	client := pc.client
	if client == nil {
		client = newSafeProviderHTTPClient()
	}
	// Apply the redirect policy to a copy so it holds even if a caller
	// injected a client that would otherwise follow redirects.
	safe := *client
	safe.CheckRedirect = refuseProviderRedirect
	resp, err := safe.Do(req)
	if err != nil {
		logProviderFailure(target.Host, err)
		if errors.Is(err, ErrProviderDestination) {
			return nil, fmt.Errorf("%w: destination not permitted", ErrProviderDestination)
		}
		return nil, fmt.Errorf("%w: could not reach provider", ErrProviderRequest)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		logProviderFailure(target.Host, err)
		return nil, fmt.Errorf("%w: could not read provider response", ErrProviderRequest)
	}
	if resp.StatusCode != http.StatusOK {
		// A status code names nothing internal and is what an admin needs to
		// fix a bad key. A redirect lands here too: it is a failure, not a
		// hop, because following it would move the key to an unchecked host.
		if redirect := resp.StatusCode/100 == 3; redirect {
			return nil, fmt.Errorf("provider list-models: redirect refused (HTTP %d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("provider list-models: HTTP %d", resp.StatusCode)
	}
	return parseModelList(resp.Header.Get("Content-Type"), body)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// parseModelList understands the two wire shapes we support today:
// OpenAI {"data":[{"id":..}]} and Anthropic {"data":[{"id":..,"display_name":..}]}.
func parseModelList(contentType string, body []byte) ([]ProviderModel, error) {
	var doc struct {
		Data []struct {
			ID          string         `json:"id"`
			DisplayName string         `json:"display_name"`
			Metadata    map[string]any `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse model list: %w", err)
	}
	out := make([]ProviderModel, 0, len(doc.Data))
	for _, m := range doc.Data {
		out = append(out, ProviderModel{ModelID: m.ID, DisplayName: m.DisplayName, Metadata: m.Metadata})
	}
	return out, nil
}
