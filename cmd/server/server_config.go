package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

const (
	defaultHTTPPort         = 3000
	defaultHTTPSPort        = 443
	defaultHTTPRedirectPort = 80
)

type serverConfig struct {
	TLSEnabled  bool
	HTTPPort    int
	HTTPSPort   int
	TLSCertFile string
	TLSKeyFile  string
	PublicHost  string
}

func loadServerConfigFromEnv() (serverConfig, error) {
	certFile := strings.TrimSpace(os.Getenv("TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("TLS_KEY_FILE"))
	publicHost := strings.TrimSpace(os.Getenv("PUBLIC_HOST"))

	if (certFile == "") != (keyFile == "") {
		return serverConfig{}, fmt.Errorf("TLS_CERT_FILE and TLS_KEY_FILE must both be set")
	}

	if certFile == "" {
		httpPort, err := loadPortEnv("PORT", defaultHTTPPort)
		if err != nil {
			return serverConfig{}, err
		}
		return serverConfig{
			TLSEnabled: false,
			HTTPPort:   httpPort,
			PublicHost: publicHost,
		}, nil
	}

	httpsPort, err := loadPortEnv("HTTPS_PORT", defaultHTTPSPort)
	if err != nil {
		return serverConfig{}, err
	}
	httpRedirectPort, err := loadPortEnv("HTTP_REDIRECT_PORT", defaultHTTPRedirectPort)
	if err != nil {
		return serverConfig{}, err
	}
	if httpRedirectPort == httpsPort {
		return serverConfig{}, fmt.Errorf("HTTP_REDIRECT_PORT must differ from HTTPS_PORT")
	}

	return serverConfig{
		TLSEnabled:  true,
		HTTPPort:    httpRedirectPort,
		HTTPSPort:   httpsPort,
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
		PublicHost:  publicHost,
	}, nil
}

func isTLSEnabledByConfig() (bool, error) {
	cfg, err := loadServerConfigFromEnv()
	if err != nil {
		return false, err
	}
	return cfg.TLSEnabled, nil
}

func buildRedirectHandler(httpsPort int, publicHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHost := redirectHost(r.Host, publicHost, httpsPort)
		targetURL := "https://" + targetHost + r.URL.RequestURI()
		http.Redirect(w, r, targetURL, http.StatusMovedPermanently)
	})
}

func redirectHost(host, publicHost string, httpsPort int) string {
	if publicHost != "" {
		return formatHostPort(strings.TrimSpace(publicHost), httpsPort)
	}
	if host == "" {
		return formatHostPort("localhost", httpsPort)
	}

	name, _, err := net.SplitHostPort(host)
	if err == nil {
		return formatHostPort(name, httpsPort)
	}

	return formatHostPort(host, httpsPort)
}

func formatHostPort(host string, port int) string {
	if port == defaultHTTPSPort {
		return host
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}

func loadPortEnv(envVar string, defaultValue int) (int, error) {
	port, err := loadPositiveIntEnv(envVar, defaultValue)
	if err != nil {
		return 0, err
	}
	if port > 65535 {
		return 0, fmt.Errorf("%s must be a valid TCP port", envVar)
	}
	return port, nil
}

func normalizedAppEnv() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
}

func isProductionEnv() bool {
	switch normalizedAppEnv() {
	case "prod", "production":
		return true
	default:
		return false
	}
}
