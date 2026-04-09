package commands

import (
	stdcontext "context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
)

const (
	promptInlineMaxLength = 6000
	promptUploadMaxBytes  = 256 * 1024
)

var promptUploadExtensions = map[string]struct{}{
	".md":       {},
	".markdown": {},
	".txt":      {},
}

var promptUploadContentTypes = map[string]struct{}{
	"text/markdown":   {},
	"text/plain":      {},
	"text/x-markdown": {},
}

func promptLength(prompt string) int {
	return len([]rune(prompt))
}

func buildPromptStoredMessage(subject, action, viewCommand, prompt string) string {
	return fmt.Sprintf(
		"✅ %s %s successfully.\nLength: %d chars.\nUse `%s` to review it.",
		subject,
		action,
		promptLength(prompt),
		viewCommand,
	)
}

func buildPromptResetMessage(subject, viewCommand string) string {
	return fmt.Sprintf(
		"✅ %s reset to default.\nUse `%s` to review it.",
		subject,
		viewCommand,
	)
}

func readPromptAttachment(ctx stdcontext.Context, attachment discord.Attachment) (string, error) {
	if attachment.URL == "" {
		return "", fmt.Errorf("prompt file is missing a download URL")
	}

	if attachment.Size > promptUploadMaxBytes {
		return "", fmt.Errorf("prompt file is too large: %d bytes (max %d bytes)", attachment.Size, promptUploadMaxBytes)
	}

	if !isSupportedPromptAttachment(attachment) {
		return "", fmt.Errorf("prompt file must be a UTF-8 .txt or .md file")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attachment.URL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create prompt file request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download prompt file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download prompt file: status %d", resp.StatusCode)
	}

	if resp.ContentLength > promptUploadMaxBytes {
		return "", fmt.Errorf("prompt file is too large: %d bytes (max %d bytes)", resp.ContentLength, promptUploadMaxBytes)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, promptUploadMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("failed to read prompt file: %w", err)
	}

	if len(data) > promptUploadMaxBytes {
		return "", fmt.Errorf("prompt file is too large: more than %d bytes", promptUploadMaxBytes)
	}

	prompt := strings.TrimPrefix(string(data), "\ufeff")
	if !utf8.ValidString(prompt) {
		return "", fmt.Errorf("prompt file must be valid UTF-8 text")
	}

	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("prompt file is empty")
	}

	return prompt, nil
}

func isSupportedPromptAttachment(attachment discord.Attachment) bool {
	if _, ok := promptUploadExtensions[strings.ToLower(filepath.Ext(attachment.Filename))]; ok {
		return true
	}

	if attachment.ContentType == nil {
		return false
	}

	contentType := strings.ToLower(strings.TrimSpace(*attachment.ContentType))
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = contentType[:idx]
	}

	_, ok := promptUploadContentTypes[contentType]
	return ok
}
