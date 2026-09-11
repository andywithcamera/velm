package security

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ProviderClient performs authenticated read-only calls against a provider's
// public API (initially: list models). It is deliberately narrow: it never
// returns raw key material and never logs headers.
type ProviderClient struct {
	HTTPClient *http.Client
}

func NewProviderClient() *ProviderClient {
	return &ProviderClient{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
}

// ProviderModel is one entry from a provider's list-models response.
type ProviderModel struct {
	ModelID     string         `json:"model_id"`
	DisplayName string         `json:"display_name"`
	Metadata    map[string]any `json:"metadata"`
}

// ListModels calls the provider's model-list endpoint with the given
// plaintext key and returns the parsed catalog. Wire specifics (auth header,
// required version headers) come from _ai_provider.provider_metadata.
func (pc *ProviderClient) ListModels(baseURL, listPath, plaintextKey string, meta map[string]any) ([]ProviderModel, error) {
	url := baseURL + listPath
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
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

	resp, err := pc.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
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
			ID           string         `json:"id"`
			DisplayName  string         `json:"display_name"`
			Metadata     map[string]any `json:"metadata"`
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
