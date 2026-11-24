package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Last council round: comment-only losses and the implicit-null list case.
func TestPatchKeepsCommentsAroundBlockScalarsAndLists(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		patch yamlPatch
		want  string
	}{
		{
			name:  "comment indented between key and block content survives a block scalar edit",
			src:   "bot:\n  default_prompt: |\n      old text\n    # note\n  trigger_words: []\n",
			patch: yamlPatch{Path: "bot.default_prompt", Kind: fieldKindText, Val: "new"},
			want:  "bot:\n  default_prompt: new\n    # note\n  trigger_words: []\n",
		},
		{
			name:  "explicit indentation indicator is honoured",
			src:   "bot:\n  default_prompt: |2\n      indented text\n  trigger_words: []\n",
			patch: yamlPatch{Path: "bot.default_prompt", Kind: fieldKindText, Val: "new"},
			want:  "bot:\n  default_prompt: new\n  trigger_words: []\n",
		},
		{
			name:  "clearing a block list keeps the key line comment",
			src:   "bot:\n  trigger_words: # keep header\n    - a\n    - b\n  default_prompt: x\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{}},
			want:  "bot:\n  trigger_words: [] # keep header\n  default_prompt: x\n",
		},
		{
			name:  "clearing an implicit-null list writes [] instead of failing",
			src:   "bot:\n  trigger_words:\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{}},
			want:  "bot:\n  trigger_words: []\nlogging:\n  level: info\n",
		},
		{
			name:  "clearing an implicit-null list with a comment keeps it",
			src:   "bot:\n  trigger_words: # keep\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{}},
			want:  "bot:\n  trigger_words: [] # keep\nlogging:\n  level: info\n",
		},
		{
			name:  "filling an implicit-null list keeps the key line comment",
			src:   "bot:\n  trigger_words: # keep\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{"a", "b"}},
			want:  "bot:\n  trigger_words: # keep\n    - a\n    - b\nlogging:\n  level: info\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := patchYAMLBytes([]byte(tc.src), []yamlPatch{tc.patch})
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))
		})
	}
}
