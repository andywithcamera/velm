package main

import (
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"velm/internal/auth"
	"velm/internal/db"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func loginPostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")
	next := sanitizeLoginNext(strings.TrimSpace(r.FormValue("next")))
	user, err := loadUserFromDB(email)
	if err != nil {
		log.Printf("Login failed for %s: user lookup failed: %v", email, err)
		handleFailedLogin(w, r)
		return
	}

	var storedPassword string
	err = db.Pool.QueryRow(context.Background(), `
        SELECT password_hash FROM _user WHERE email = $1
    `, email).Scan(&storedPassword)

	if err != nil {
		// Either no user or DB error — both mean bad login
		log.Printf("Login failed for %s: %v", email, err)
		handleFailedLogin(w, r)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(storedPassword), []byte(password))
	if err == nil {
		if next == "/" {
			next = defaultAuthenticatedRoute(r.Context())
		}

		sessionVersion, versionErr := db.GetOrInitUserSessionVersion(context.Background(), user.ID)
		if versionErr != nil {
			http.Error(w, "Failed to initialize session security", http.StatusInternalServerError)
			return
		}

		// Password is correct
		// Invalidate any pre-login cookie and issue a fresh authenticated session.
		oldSession, _ := store.Get(r, "mysession")
		oldSession.Options.MaxAge = -1
		_ = oldSession.Save(r, w)

		session, _ := store.New(r, "mysession")
		session.Options = store.Options
		session.Values["authenticated"] = true
		session.Values["user_id"] = user.ID
		session.Values["user_email"] = user.Email
		session.Values["user_name"] = user.Name
		session.Values["user_role"] = strings.ToLower(strings.TrimSpace(user.Role))
		session.Values["session_version"] = sessionVersion
		session.Values["csrf_token"] = base64.StdEncoding.EncodeToString(auth.GenerateRandomKey(32))
		if err := session.Save(r, w); err != nil {
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}

		if r.Header.Get("HX-Request") != "" {
			w.Header().Set("HX-Redirect", next)
			return
		}
		http.Redirect(w, r, next, http.StatusFound)
		return
	}

	// Password mismatch
	handleFailedLogin(w, r)
}
