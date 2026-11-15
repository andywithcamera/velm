package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #352: geometry and typography moved behind tokens so a style pack can
// reshape the app without touching every rule. Two things can go wrong silently
// once values are indirect, and neither shows up in a browser until someone
// looks at the right page:
//
//  1. a stylesheet references a token nothing defines, so the property is
//     dropped and the element renders with the initial value (square corners,
//     no border);
//  2. a rewritten declaration loses its semicolon, so the next declaration is
//     swallowed into the value and *both* properties are dropped.
//
// The second one was real: an early tokenisation pass emitted
// `border-radius: var(--radius-4xl)` with no trailing semicolon, which would
// have squared off every card in the app. These tests are the guard.

var (
	tokenDef = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	tokenRef = regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)`)
	// Style-axis tokens only. Colour tokens are spread across theme blocks and
	// are allowed to be defined per theme.
	styleTokenPrefixes = []string{"--radius-", "--edge-", "--font-"}
	// A property declaration must begin a statement: the last non-space
	// character before it must be ';', '{' or '}'.
	propertyLine = regexp.MustCompile(`(?m)^[ \t]*([a-z-]+)\s*:`)
	cssComment   = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// stripComments blanks comments out while keeping every byte offset (and so
// every line number) intact, so prose inside a comment cannot look like a
// declaration.
func stripComments(source string) string {
	return cssComment.ReplaceAllStringFunc(source, func(comment string) string {
		return strings.Repeat(" ", len(comment))
	})
}

func uiStylesheets(t *testing.T) []string {
	t.Helper()
	t.Chdir("../..")
	sheets, err := filepath.Glob(filepath.Join("web", "static", "css", "ui", "*.css"))
	if err != nil {
		t.Fatalf("glob ui css: %v", err)
	}
	sheets = append(sheets, filepath.Join("web", "static", "css", "ui.css"))
	if len(sheets) < 10 {
		t.Fatalf("found %d stylesheets; glob is wrong", len(sheets))
	}
	return sheets
}

func TestStyleTokensAreDefined(t *testing.T) {
	sheets := uiStylesheets(t)

	defined := map[string]bool{}
	for _, sheet := range sheets {
		source, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("read %s: %v", sheet, err)
		}
		for _, m := range tokenDef.FindAllStringSubmatch(string(source), -1) {
			defined[m[1]] = true
		}
	}

	used := map[string][]string{}
	for _, sheet := range sheets {
		source, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("read %s: %v", sheet, err)
		}
		for _, m := range tokenRef.FindAllStringSubmatch(string(source), -1) {
			name := m[1]
			for _, prefix := range styleTokenPrefixes {
				if strings.HasPrefix(name, prefix) {
					used[name] = append(used[name], filepath.Base(sheet))
				}
			}
		}
	}
	if len(used) < 5 {
		t.Fatalf("found only %d style-token references; the parser is not seeing the stylesheets", len(used))
	}

	for name, where := range used {
		if !defined[name] {
			t.Errorf("%s is used in %s but never defined; every rule that reads it silently loses the property",
				name, strings.Join(unique(where), ", "))
		}
	}
}

// TestDeclarationsEndWithSemicolon catches a declaration that runs into the next
// one. CSS recovery rules make this a whole-block failure rather than a parse
// error, so it is invisible to anything that only checks the file is readable.
func TestDeclarationsEndWithSemicolon(t *testing.T) {
	for _, sheet := range uiStylesheets(t) {
		source, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("read %s: %v", sheet, err)
		}
		text := stripComments(string(source))
		line := 1
		for _, loc := range propertyLine.FindAllStringSubmatchIndex(text, -1) {
			property := text[loc[2]:loc[3]]
			if strings.HasPrefix(property, "--") {
				continue // a custom property definition, same rule applies
			}
			line = 1 + strings.Count(text[:loc[2]], "\n")
			before := strings.TrimRight(text[:loc[2]], " \t\r\n")
			if before == "" {
				continue
			}
			last := before[len(before)-1]
			if last == ';' || last == '{' || last == '}' || last == ',' ||
				last == '>' || last == '+' || last == '~' || last == '*' {
				continue
			}
			t.Errorf("%s:%d: %q starts a declaration but the previous character is %q, not ';'; "+
				"the parser will swallow this and the preceding declaration", filepath.Base(sheet), line, property, string(last))
		}
	}
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
