package commands

import (
	stdcontext "context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadPromptAttachment_SupportsMarkdownByExtension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# prompt\nbe nice"))
	}))
	defer server.Close()

	attachment := discord.Attachment{
		Filename: "prompt.md",
		Size:     len("# prompt\nbe nice"),
		URL:      server.URL,
	}

	prompt, err := readPromptAttachment(stdcontext.Background(), attachment)
	require.NoError(t, err)
	assert.Equal(t, "# prompt\nbe nice", prompt)
}

func TestReadPromptAttachment_RejectsUnsupportedType(t *testing.T) {
	contentType := "application/pdf"
	attachment := discord.Attachment{
		Filename:    "prompt.pdf",
		ContentType: &contentType,
		URL:         "https://example.com/prompt.pdf",
	}

	_, err := readPromptAttachment(stdcontext.Background(), attachment)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".txt or .md")
}

func TestReadPromptAttachment_RejectsOversizedBody(t *testing.T) {
	body := strings.Repeat("a", promptUploadMaxBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	attachment := discord.Attachment{
		Filename: "prompt.txt",
		URL:      server.URL,
	}

	_, err := readPromptAttachment(stdcontext.Background(), attachment)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestBuildPromptStoredMessage_IncludesLengthAndViewCommand(t *testing.T) {
	message := buildPromptStoredMessage("Prompt", "set", "/prompt see", "hello")

	assert.Contains(t, message, "Prompt set successfully")
	assert.Contains(t, message, "Length: 5 chars")
	assert.Contains(t, message, "/prompt see")
}
