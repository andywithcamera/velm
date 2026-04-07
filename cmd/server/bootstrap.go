package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
	"velm/internal/db"
	"velm/internal/security"

	"github.com/gorilla/sessions"
)

func initSessionStore() error {
	authKey, err := loadSessionKeyFromEnv("SESSION_AUTH_KEY")
	if err != nil {
		return err
	}
	encryptionKey, err := loadSessionKeyFromEnv("SESSION_ENCRYPTION_KEY")
	if err != nil {
		return err
	}
	maxAgeSeconds, err := loadSessionMaxAgeSecondsFromEnv()
	if err != nil {
		return err
	}
	cookieSecure, err := loadSessionCookieSecureFromEnv()
	if err != nil {
		return err
	}
	store = sessions.NewCookieStore(authKey, encryptionKey)
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		HttpOnly: true,
		Secure:   cookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
	return nil
}

func initTemplates() {
	templates = template.Must(template.ParseGlob("web/templates/*.html"))
	template.Must(templates.ParseGlob("web/templates/components/*.html"))
}

func initDatabase() error {
	if err := db.ConnectToDB(); err != nil {
		return fmt.Errorf("error connecting to the database: %w", err)
	}
	if err := db.RunMigrations(context.Background()); err != nil {
		return fmt.Errorf("error running migrations: %w", err)
	}
	if err := bootstrapInitialUserIfConfigured(context.Background()); err != nil {
		return fmt.Errorf("error bootstrapping initial user: %w", err)
	}
	if err := loadPropertiesFromDB(); err != nil {
		return fmt.Errorf("error loading properties from DB: %w", err)
	}
	if err := loadMenuFromDB(); err != nil {
		return fmt.Errorf("error loading menu from DB: %w", err)
	}
	return nil
}

func startServer() {
	registerRoutes()

	cfg, err := loadServerConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	rootHandler := security.WithRequestID(
		security.Monitor(
			security.SecurityHeaders(
				security.RecoverPanic(http.DefaultServeMux),
			),
			store,
		),
	)

	if cfg.TLSEnabled {
		if cfg.HTTPPort > 0 {
			go func() {
				redirectAddr := fmt.Sprintf(":%d", cfg.HTTPPort)
				if err := http.ListenAndServe(redirectAddr, buildRedirectHandler(cfg.HTTPSPort, cfg.PublicHost)); err != nil {
					log.Fatal(err)
				}
			}()
		}

		httpsAddr := fmt.Sprintf(":%d", cfg.HTTPSPort)
		log.Printf("Server started at https://%s%s", displayHost(cfg.PublicHost), displayPortSuffix(cfg.HTTPSPort))
		if cfg.HTTPPort > 0 {
			log.Printf(
				"Redirecting http://%s%s to https://%s%s",
				displayHost(cfg.PublicHost),
				displayPortSuffix(cfg.HTTPPort),
				displayHost(cfg.PublicHost),
				displayPortSuffix(cfg.HTTPSPort),
			)
		}

		server := &http.Server{
			Addr:              httpsAddr,
			Handler:           rootHandler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		if err := server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
			log.Fatal(err)
		}
		return
	}

	httpAddr := fmt.Sprintf(":%d", cfg.HTTPPort)
	log.Printf("Server started at http://%s%s", displayHost(cfg.PublicHost), displayPortSuffix(cfg.HTTPPort))
	server := &http.Server{
		Addr:              httpAddr,
		Handler:           rootHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func displayPortSuffix(port int) string {
	if port == defaultHTTPSPort || port == defaultHTTPRedirectPort {
		return ""
	}
	return fmt.Sprintf(":%d", port)
}

func displayHost(host string) string {
	if host == "" {
		return "localhost"
	}
	return host
}
