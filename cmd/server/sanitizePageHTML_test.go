package main

import (
	"strings"
	"testing"
)

// TestSanitizePageHTMLStripsScriptExecution covers every payload the security
// audit (#135) demonstrated as passing the old regex sanitiser, plus the one it
// did block. All must emerge inert.
func TestSanitizePageHTMLStripsScriptExecution(t *testing.T) {
	payloads := []string{
		`<img src=x onerror="alert(1)">`,
		`<img src=x onerror='alert(1)'>`,
		`<img src=x onerror=alert(1)>`,
		`<svg/onload=alert(1)>`,
		`<body onload=alert(1)>`,
		`<a href='javascript:alert(1)'>x</a>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<a href=javascript:alert(1)>x</a>`,
		`<iframe srcdoc='&lt;script&gt;alert(1)&lt;/script&gt;'></iframe>`,
		`<math><a xlink:href=x onerror=alert(1)>x</a></math>`,
		`<script>alert(1)</script>`,
		`<script src="//evil.example/x.js"></script>`,
		`<style>body{display:none}</style>`,
		`<scr<script>ipt>alert(1)</script>`,
		`<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=">`,
		`<form action="//evil.example"><input name=x></form>`,
		`<a href="jav&#x09;ascript:alert(1)">x</a>`,
	}

	// Substrings that would indicate a live script vector survived.
	forbidden := []string{
		"<script", "onerror", "onload", "javascript:", "srcdoc", "<iframe", "<style", "<form",
	}

	for _, payload := range payloads {
		out := sanitizePageHTML(payload)
		lower := strings.ToLower(out)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("payload %q produced %q, which still contains %q", payload, out, bad)
			}
		}
	}
}

// TestSanitizePageHTMLEscalationPayloadsFromIssue renders the exact vectors that
// matter most for the Write -> Admin escalation path.
func TestSanitizePageHTMLEscalationPayloadsFromIssue(t *testing.T) {
	for _, payload := range []string{
		`<svg onload=alert(document.domain)>`,
		`<div onmouseover='alert(1)'>hover</div>`,
		`<details open ontoggle=alert(1)>`,
		`<img src=1 onerror=alert(1)//>`,
	} {
		out := sanitizePageHTML(payload)
		if strings.Contains(strings.ToLower(out), "alert") {
			t.Errorf("payload %q survived as %q", payload, out)
		}
	}
}

func TestSanitizePageHTMLKeepsBenignMarkup(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{
			name: "bold",
			in:   `<b>bold</b>`,
			want: []string{"<b>bold</b>"},
		},
		{
			name: "link",
			in:   `<a href="https://example.com/page">example</a>`,
			want: []string{`<a href="https://example.com/page"`, "example</a>"},
		},
		{
			name: "section and headings",
			in:   `<section><h1>Welcome</h1><p>Body <code>code</code></p></section>`,
			want: []string{"<section>", "<h1>Welcome</h1>", "<p>Body <code>code</code></p>", "</section>"},
		},
		{
			name: "lists and tables",
			in:   `<ul><li>one</li></ul><table><tr><td>a</td></tr></table>`,
			want: []string{"<ul>", "<li>one</li>", "<table>", "<td>a</td>"},
		},
		{
			name: "class and id survive",
			in:   `<div class="ui-cards grid" id="panel-1">x</div>`,
			want: []string{`class="ui-cards grid"`, `id="panel-1"`},
		},
		{
			name: "plain text",
			in:   `Core work management page.`,
			want: []string{"Core work management page."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitizePageHTML(tc.in)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("sanitizePageHTML(%q) = %q, want it to contain %q", tc.in, out, want)
				}
			}
		})
	}
}

func TestSanitizePageHTMLIsIdempotent(t *testing.T) {
	in := `<section class="ui-cards"><h1>Hi</h1><img src=x onerror=alert(1)><a href="https://example.com">link</a></section>`
	once := sanitizePageHTML(in)
	twice := sanitizePageHTML(once)
	if once != twice {
		t.Errorf("not idempotent:\n once: %q\ntwice: %q", once, twice)
	}
}
