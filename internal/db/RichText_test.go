package db

import (
	"strings"
	"testing"
)

func TestSanitizeRichTextHTMLStripsUnsafeMarkup(t *testing.T) {
	input := `<p onclick="alert(1)">Hello <strong>world</strong><script>alert(2)</script> <a href="javascript:alert(3)" style="color:red">bad</a> <a href="https://example.com/docs" onclick="alert(4)">safe</a></p>`
	got := SanitizeRichTextHTML(input)

	if strings.Contains(got, "<script") {
		t.Fatalf("expected script tags to be removed, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "onclick") {
		t.Fatalf("expected event handler attributes to be removed, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Fatalf("expected unsafe javascript url to be removed, got %q", got)
	}
	if !strings.Contains(got, `<a href="https://example.com/docs" target="_blank" rel="noopener noreferrer">safe</a>`) {
		t.Fatalf("expected safe link to remain with enforced rel/target, got %q", got)
	}
}
