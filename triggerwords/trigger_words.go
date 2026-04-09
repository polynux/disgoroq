package triggerwords

import (
	"fmt"
	"regexp"
	"strings"
)

var wordPattern = regexp.MustCompile(`^[\p{L}\p{N}_-]+$`)
var tokenPattern = regexp.MustCompile(`[\p{L}\p{N}_-]+`)

func Normalize(word string) string {
	return strings.ToLower(strings.TrimSpace(word))
}

func NormalizeAll(words []string) []string {
	normalized := make([]string, 0, len(words))
	seen := make(map[string]struct{}, len(words))
	for _, word := range words {
		cleaned := Normalize(word)
		if cleaned == "" {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func Parse(input string) []string {
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	return NormalizeAll(parts)
}

func Validate(words []string) error {
	for _, word := range words {
		normalized := Normalize(word)
		if normalized == "" {
			return fmt.Errorf("trigger words cannot be empty")
		}
		if !wordPattern.MatchString(normalized) {
			return fmt.Errorf("invalid trigger word %q: use only letters, numbers, underscores, or dashes", word)
		}
	}
	return nil
}

func Contains(message string, triggerWords []string) bool {
	if len(triggerWords) == 0 {
		return false
	}

	normalizedWords := NormalizeAll(triggerWords)
	if len(normalizedWords) == 0 {
		return false
	}

	wordSet := make(map[string]struct{}, len(normalizedWords))
	for _, word := range normalizedWords {
		wordSet[word] = struct{}{}
	}

	for _, token := range tokenPattern.FindAllString(strings.ToLower(message), -1) {
		if _, ok := wordSet[token]; ok {
			return true
		}
	}

	return false
}
