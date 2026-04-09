package triggerwords

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_NormalizesAndDeduplicates(t *testing.T) {
	words := Parse(" Feun,feunboy,\nFEUN ")

	assert.Equal(t, []string{"feun", "feunboy"}, words)
}

func TestValidate_RejectsWhitespace(t *testing.T) {
	err := Validate([]string{"two words"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid trigger word")
}

func TestContains_UsesWholeWords(t *testing.T) {
	assert.True(t, Contains("salut feun, ça va ?", []string{"feun"}))
	assert.False(t, Contains("salut superfeun", []string{"feun"}))
}
