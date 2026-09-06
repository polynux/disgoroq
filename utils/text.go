package utils

import (
	"strings"
)

var botTextReplacer = strings.NewReplacer("feur", "fleur")

// NormalizeBotText applies shared post-processing to bot-generated text before delivery.
func NormalizeBotText(text string) string {
	return CollapseBlankLines(botTextReplacer.Replace(text))
}

// CollapseBlankLines strips blank lines (and repeated newlines) that the
// model sometimes inserts between sentences, keeping at most one newline
// between paragraphs and trimming the ends.
func CollapseBlankLines(text string) string {
	if !strings.Contains(text, "\n") {
		return strings.TrimSpace(text)
	}

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := true // treats leading blanks as trimmed
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		blank := strings.TrimSpace(trimmed) == ""
		if blank {
			if prevBlank {
				continue
			}
			// Collapse to a single newline: mark the break with an empty
			// line; consecutive blank lines are deduplicated by prevBlank.
			out = append(out, "")
			prevBlank = true
			continue
		}
		out = append(out, trimmed)
		prevBlank = false
	}

	// Trim trailing paragraph breaks
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 {
		// Drop paragraph-break markers entirely: join non-empty lines with a
		// single newline so no blank line survives anywhere.
		compact := make([]string, 0, len(out))
		for _, line := range out {
			if line != "" {
				compact = append(compact, line)
			}
		}
		out = compact
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
