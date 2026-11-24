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
			name:  "multi-line replacement of a block scalar keeps the comment below it a comment",
			src:   "bot:\n  default_prompt: |\n      old\n    # note\n  trigger_words: []\n",
			patch: yamlPatch{Path: "bot.default_prompt", Kind: fieldKindText, Val: "line1\nline2"},
			want:  "bot:\n  default_prompt: |-\n    line1\n    line2\n  # note\n  trigger_words: []\n",
		},
		{
			name:  "multi-line replacement of a plain scalar keeps a deeper comment below it a comment",
			src:   "bot:\n  default_prompt: short\n    # note\n  trigger_words: []\n",
			patch: yamlPatch{Path: "bot.default_prompt", Kind: fieldKindText, Val: "line1\nline2"},
			want:  "bot:\n  default_prompt: |-\n    line1\n    line2\n  # note\n  trigger_words: []\n",
		},
		{
			name: "mixed content indents under an explicit indicator",
			// keyIndent 2 + indicator 2 = content indent 4: lines at 7 and 5 are
			// content, the comment at 3 is not. Ignoring the indicator (first line
			// sets indent 7) would end the block at "b"; the old "> keyIndent"
			// rule would swallow the comment.
			src:   "bot:\n  default_prompt: |2\n       a\n     b\n   # note\n  trigger_words: []\n",
			patch: yamlPatch{Path: "bot.default_prompt", Kind: fieldKindText, Val: "new"},
			want:  "bot:\n  default_prompt: new\n   # note\n  trigger_words: []\n",
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
			name:  "filling an explicit null list keeps the key line comment",
			src:   "bot:\n  trigger_words: null # keep\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{"a"}},
			want:  "bot:\n  trigger_words: # keep\n    - a\nlogging:\n  level: info\n",
		},
		{
			name:  "filling a quoted empty list value keeps the comment, not a quoted hash",
			src:   "bot:\n  trigger_words: '' # keep\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{"a"}},
			want:  "bot:\n  trigger_words: # keep\n    - a\nlogging:\n  level: info\n",
		},
		{
			name:  "a hash inside a quoted value is not turned into a comment",
			src:   "bot:\n  trigger_words: \"a # b\"\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{"a"}},
			want:  "bot:\n  trigger_words:\n    - a\nlogging:\n  level: info\n",
		},
		{
			name:  "filling an implicit-null list keeps the key line comment",
			src:   "bot:\n  trigger_words: # keep\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{"a", "b"}},
			want:  "bot:\n  trigger_words: # keep\n    - a\n    - b\nlogging:\n  level: info\n",
		},
		{
			name:  "inserting a multi-line field above a commented-out list item",
			src:   "bot:\n  trigger_words:\n    - foo\n    # - bar\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "bot.default_prompt", Kind: fieldKindText, Val: "line1\nline2"},
			want:  "bot:\n  trigger_words:\n    - foo\n  default_prompt: |-\n    line1\n    line2\n  # - bar\nlogging:\n  level: info\n",
		},
		{
			name:  "inserting a multi-line field into an empty section with a deep comment",
			src:   "votes:\n      # deep\nlogging:\n  level: info\n",
			patch: yamlPatch{Path: "votes.good_emoji", Kind: fieldKindText, Val: "line1\nline2"},
			want:  "votes:\n  good_emoji: |-\n    line1\n    line2\n  # deep\nlogging:\n  level: info\n",
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
