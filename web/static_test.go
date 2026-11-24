package web

import (
	"strings"
	"testing"
)

// TestStyleSheetBracesBalanced guards against an unclosed rule: browsers with
// CSS nesting would silently nest every following rule inside it.
func TestStyleSheetBracesBalanced(t *testing.T) {
	data, err := embeddedFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	depth := 0
	for i, line := range strings.Split(string(data), "\n") {
		// A rule written at column 0 must start at top level; nested rules
		// (inside @media) are indented.
		if strings.Contains(line, "{") && line != "" && line[0] != ' ' && line[0] != '\t' && depth != 0 {
			t.Fatalf("style.css line %d: top-level rule starts at brace depth %d (previous rule not closed)", i+1, depth)
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			t.Fatalf("style.css line %d: extra closing brace", i+1)
		}
	}
	if depth != 0 {
		t.Fatalf("style.css ends with brace depth %d", depth)
	}
}
