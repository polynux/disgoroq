package utils

import "testing"

func TestNormalizeBotText(t *testing.T) {
	input := "feur bonjour, coiffeur et feur"
	got := NormalizeBotText(input)

	if got != "fleur bonjour, coiffleur et fleur" {
		t.Fatalf("expected normalized text, got %q", got)
	}
}
