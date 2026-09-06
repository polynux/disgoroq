package utils

import "testing"

func TestNormalizeBotText(t *testing.T) {
	input := "feur bonjour, coiffeur et feur"
	got := NormalizeBotText(input)

	if got != "fleur bonjour, coiffleur et fleur" {
		t.Fatalf("expected normalized text, got %q", got)
	}
}

func TestNormalizeBotText_CollapsesBlankLines(t *testing.T) {
	// Multi-line answers from the model use blank lines between every
	// sentence; collapse them so Discord messages stay compact.
	in := "oh les goats du dubstep :superes:\n\nça envoie du lourd :criminel:\n\nfaut que j'check ça :steamhappy~2: \n"
	got := NormalizeBotText(in)

	want := "oh les goats du dubstep :superes:\nça envoie du lourd :criminel:\nfaut que j'check ça :steamhappy~2:"
	if got != want {
		t.Fatalf("expected collapsed newlines, got %q", got)
	}
}

func TestNormalizeBotText_CollapsesRepeatedBlankLines(t *testing.T) {
	if got, want := NormalizeBotText("a\n\n\n\nb"), "a\nb"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeBotText_KeepsSingleNewlines(t *testing.T) {
	if got, want := NormalizeBotText("ligne1\nligne2"), "ligne1\nligne2"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeBotText_TrimsTrailingBlanks(t *testing.T) {
	if got, want := NormalizeBotText("phrase\n\n\n"), "phrase"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
