package commands

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitPromptPages_PreservesContentAndLimits(t *testing.T) {
	prompt := strings.Repeat("alpha beta gamma\n", 40)

	pages := splitPromptPages(prompt, 80)

	require.Greater(t, len(pages), 1)
	assert.Equal(t, prompt, strings.Join(pages, ""))
	for _, page := range pages {
		assert.LessOrEqual(t, len([]rune(page)), 80)
	}
}

func TestSplitPromptPages_EmptyPrompt(t *testing.T) {
	assert.Equal(t, []string{"(empty prompt)"}, splitPromptPages("", 80))
}

func TestBuildPromptEmbeds_AddsPaginationMetadata(t *testing.T) {
	pages := []string{"first page", "second page"}

	embeds := buildPromptEmbeds("Current prompt", "first pagesecond page", pages)

	require.Len(t, embeds, 2)
	assert.Equal(t, "Current prompt", embeds[0].Title)
	assert.Equal(t, "first page", embeds[0].Description)
	require.NotNil(t, embeds[0].Footer)
	assert.Contains(t, embeds[0].Footer.Text, "Page 1/2")
	assert.Contains(t, embeds[1].Footer.Text, "Page 2/2")
	assert.Contains(t, embeds[0].Footer.Text, "21 chars")
}
