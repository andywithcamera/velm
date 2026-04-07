package main

import "testing"

func TestLoadServerConfigDefaultsToHTTPOnPort3000(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("TLS_CERT_FILE", "")
	t.Setenv("TLS_KEY_FILE", "")
	t.Setenv("PORT", "")

	cfg, err := loadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("loadServerConfigFromEnv returned error: %v", err)
	}
	if cfg.TLSEnabled {
		t.Fatal("expected TLS to be disabled without cert and key")
	}
	if cfg.HTTPPort != defaultHTTPPort {
		t.Fatalf("HTTP port = %d, want %d", cfg.HTTPPort, defaultHTTPPort)
	}
}

func TestLoadServerConfigEnablesTLSWithDefaultPorts(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("TLS_CERT_FILE", "/tmp/dev-cert.pem")
	t.Setenv("TLS_KEY_FILE", "/tmp/dev-key.pem")
	t.Setenv("HTTPS_PORT", "")
	t.Setenv("HTTP_REDIRECT_PORT", "")

	cfg, err := loadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("loadServerConfigFromEnv returned error: %v", err)
	}
	if !cfg.TLSEnabled {
		t.Fatal("expected TLS to be enabled")
	}
	if cfg.HTTPSPort != defaultHTTPSPort {
		t.Fatalf("HTTPS port = %d, want %d", cfg.HTTPSPort, defaultHTTPSPort)
	}
	if cfg.HTTPPort != defaultHTTPRedirectPort {
		t.Fatalf("HTTP redirect port = %d, want %d", cfg.HTTPPort, defaultHTTPRedirectPort)
	}
}

func TestLoadServerConfigRejectsPartialTLSConfig(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("TLS_CERT_FILE", "/tmp/dev-cert.pem")
	t.Setenv("TLS_KEY_FILE", "")

	if _, err := loadServerConfigFromEnv(); err == nil {
		t.Fatal("expected partial TLS config to fail")
	}
}

func TestLoadServerConfigRejectsConflictingTLSPorts(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("TLS_CERT_FILE", "/tmp/dev-cert.pem")
	t.Setenv("TLS_KEY_FILE", "/tmp/dev-key.pem")
	t.Setenv("HTTPS_PORT", "443")
	t.Setenv("HTTP_REDIRECT_PORT", "443")

	if _, err := loadServerConfigFromEnv(); err == nil {
		t.Fatal("expected conflicting redirect and TLS ports to fail")
	}
}

func TestLoadSessionCookieSecureDefaultsToTLSMode(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("SESSION_COOKIE_SECURE", "")
	t.Setenv("TLS_CERT_FILE", "/tmp/dev-cert.pem")
	t.Setenv("TLS_KEY_FILE", "/tmp/dev-key.pem")

	secure, err := loadSessionCookieSecureFromEnv()
	if err != nil {
		t.Fatalf("loadSessionCookieSecureFromEnv returned error: %v", err)
	}
	if !secure {
		t.Fatal("expected secure cookies when TLS is configured")
	}
}

func TestLoadSessionCookieSecureDefaultsToEnabledInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_COOKIE_SECURE", "")
	t.Setenv("TLS_CERT_FILE", "")
	t.Setenv("TLS_KEY_FILE", "")

	secure, err := loadSessionCookieSecureFromEnv()
	if err != nil {
		t.Fatalf("loadSessionCookieSecureFromEnv returned error: %v", err)
	}
	if !secure {
		t.Fatal("expected secure cookies to default to enabled in production")
	}
}

func TestRedirectHostDropsHTTPPortAndUsesHTTPSPort(t *testing.T) {
	got := redirectHost("localhost:80", "", 443)
	if got != "localhost" {
		t.Fatalf("redirectHost = %q, want %q", got, "localhost")
	}

	got = redirectHost("localhost:8080", "", 8443)
	if got != "localhost:8443" {
		t.Fatalf("redirectHost = %q, want %q", got, "localhost:8443")
	}
}

func TestRedirectHostUsesCanonicalPublicHostWhenSet(t *testing.T) {
	got := redirectHost("localhost:80", "app.example.com", 443)
	if got != "app.example.com" {
		t.Fatalf("redirectHost = %q, want %q", got, "app.example.com")
	}
}

func TestLoadServerConfigAllowsHTTPInProductionWhenTLSIsTerminatedUpstream(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("TLS_CERT_FILE", "")
	t.Setenv("TLS_KEY_FILE", "")
	t.Setenv("PORT", "3000")

	cfg, err := loadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("loadServerConfigFromEnv returned error: %v", err)
	}
	if cfg.TLSEnabled {
		t.Fatal("expected in-app TLS to remain disabled without certs")
	}
	if cfg.HTTPPort != 3000 {
		t.Fatalf("HTTP port = %d, want 3000", cfg.HTTPPort)
	}
}
