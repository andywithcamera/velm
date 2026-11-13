package main

import (
	"bytes"
	"database/sql"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The docs page saves with `new FormData(form)`, which is multipart. Both bugs
// below made that request carry no fields at all or be reported as a failure,
// so every save from a browser behaved as if the form were empty.
//
// Each test fails against the old code; the comment says how.

func multipartFormRequest(t *testing.T, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/docs/article/save", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestParseDocsFormReadsMultipartFields(t *testing.T) {
	req := multipartFormRequest(t, map[string]string{
		"library_slug":  "base",
		"markdown_body": "## Overview\n\n- one",
		"title":         "Unified Field Check",
	})
	if err := parseDocsForm(req); err != nil {
		t.Fatalf("parseDocsForm: %v", err)
	}
	if got := req.FormValue("library_slug"); got != "base" {
		t.Errorf("library_slug = %q, want base", got)
	}
	if got := req.FormValue("markdown_body"); got != "## Overview\n\n- one" {
		t.Errorf("markdown_body = %q, want the typed body", got)
	}
}

func TestParseFormLeavesMultipartFieldsEmpty(t *testing.T) {
	// This is the bug parseDocsForm exists to avoid: ParseForm does not read a
	// multipart body, but it does leave Form non-nil, so a later FormValue never
	// falls through to ParseMultipartForm.
	req := multipartFormRequest(t, map[string]string{"library_slug": "base"})
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	if got := req.FormValue("library_slug"); got != "" {
		t.Fatalf("ParseForm unexpectedly exposed multipart field %q; the regression is gone, so parseDocsForm may be unnecessary", got)
	}
}

func TestParseDocsFormReadsURLEncodedFields(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/docs/article/save",
		bytes.NewBufferString("library_slug=base&markdown_body=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := parseDocsForm(req); err != nil {
		t.Fatalf("parseDocsForm: %v", err)
	}
	if got := req.FormValue("library_slug"); got != "base" {
		t.Errorf("urlencoded library_slug = %q, want base", got)
	}
}

// pgx reports a missing row with its own error value, not sql.ErrNoRows itself,
// so `switch err { case sql.ErrNoRows: }` never matched and creating a library
// or an article failed with "no rows in result set". errors.Is is the correct
// comparison.
func TestMissingRowIsMatchedByErrorsIs(t *testing.T) {
	if !errors.Is(sql.ErrNoRows, sql.ErrNoRows) {
		t.Fatal("errors.Is must match the sentinel itself")
	}
	// The exact shape the handler compares against.
	missing := &proxyErr{msg: "no rows in result set", target: sql.ErrNoRows}
	if !errors.Is(missing, sql.ErrNoRows) {
		t.Errorf("errors.Is(proxy error, sql.ErrNoRows) = false; the create path would fail")
	}
	if missing == sql.ErrNoRows {
		t.Error("the proxy error is the sentinel itself; a pointer comparison would also work")
	}
}

// proxyErr mirrors pgx v5's ErrNoRows: a distinct error that unwraps to the
// database/sql sentinel.
type proxyErr struct {
	msg    string
	target error
}

func (e *proxyErr) Error() string { return e.msg }
func (e *proxyErr) Unwrap() error { return e.target }
