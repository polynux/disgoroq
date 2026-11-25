package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
)

// Completion mode serves raw chat-log models (e.g. luciole-1b-discord1 from
// discord-ia-waldo): models trained on plain "name: text" Discord lines, not on
// instructions. They cannot follow a system prompt or call tools, so the
// request becomes the recent chat as lines ending with the bot's speaker tag
// ("§bot:" for Luciole), sent to /api/generate with raw=true, and the reply is
// that one line.

const (
	// Luciole has a 1,024-token context; French chat averages about 2.8 chars/token.
	completionPromptChars      = 2400
	completionLineChars        = 300
	completionBotLineChars     = 200
	completionMinSentenceRunes = 20
	completionMaxTokens        = 64
	completionDefaultBotLines  = 8
)

var (
	completionUserMention    = regexp.MustCompile(`<@!?(\d+)>`)
	completionDiscordEmoji   = regexp.MustCompile(`<a?:(\w+):\d+>`)
	completionPlaceholder    = regexp.MustCompile(`(?i)\[(?:gif|lien|image|youtube|tweet|tiktok|insta|son|steam|video|fichier|sticker)(?::[^\]]*)?\]`)
	completionReplyMention   = regexp.MustCompile(`@\S+`)
	completionLeadingSpeaker = regexp.MustCompile(`^[^:\s]{1,32}:(?:\s|$)`)
)

type ollamaGenerateResponse struct {
	Model           string `json:"model"`
	Response        string `json:"response"`
	Error           string `json:"error,omitempty"`
	DoneReason      string `json:"done_reason,omitempty"`
	EvalCount       int    `json:"eval_count,omitempty"`
	PromptEvalCount int    `json:"prompt_eval_count,omitempty"`
}

// MediaResolveRequest carries the raw completion reply and context needed
// to resolve media tags into real URLs from the media library.
type MediaResolveRequest struct {
	RawReply        string
	GuildID         string
	ChannelID       string
	BotID           string
	TargetText      string // v1-rendered text of the message being answered
	TargetMessageID string // non-empty when answering a specific message (empty for reengage)
}

// MediaResolveResult holds the resolved media URL and match details.
type MediaResolveResult struct {
	Tag        string
	URL        string
	Similarity float64
}

// MediaResolver resolves media tags in raw completion replies.
type MediaResolver interface {
	Resolve(ctx context.Context, req MediaResolveRequest) (MediaResolveResult, bool)
}

// SetMediaResolver attaches an optional media library resolver to completion mode.
func (o *OllamaProvider) SetMediaResolver(resolver MediaResolver) {
	o.mediaResolver = resolver
}

// SetCompletionMode switches chat requests to raw chat-log completion.
func (o *OllamaProvider) SetCompletionMode(enabled bool) {
	o.completion = enabled
}

// CompletionMode reports whether raw chat-log completion is enabled.
func (o *OllamaProvider) CompletionMode() bool {
	return o.completion
}

func (o *OllamaProvider) SetCompletionConfig(speaker string, promptChars, botLines int, mediaTags string) {
	o.completionSpeaker = speaker
	if promptChars > 0 {
		o.completionPromptChars = promptChars
	}
	if botLines > 0 {
		o.completionBotLines = botLines
	}
	o.completionMediaTags = mediaTagsVersion(mediaTags)
}

func (o *OllamaProvider) completionChat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Completion mode decides on its own whether to speak; answering another
	// bot or a webhook is never wanted. Chat mode keeps its current behaviour.
	if req.TargetFromBot || completionTargetIsBot(req.Messages, req.TargetMessageID) {
		return o.emptyCompletion(req), nil
	}
	names := completionNames(req.Messages)
	// The target must be present and render to something; answering without it
	// would make the model answer the previous line.
	if req.TargetMessageID != "" {
		target, ok := completionTargetMessage(req.Messages, req.TargetMessageID)
		if !ok || strings.TrimSpace(completionText(target, names, o.completionMediaTags)) == "" {
			return o.emptyCompletion(req), nil
		}
	}
	targetV1Text := completionTargetV1Text(req.Messages, req.TargetMessageID)
	maxTokens := req.MaxTokens
	if maxTokens <= 0 || maxTokens > completionMaxTokens {
		maxTokens = completionMaxTokens
	}
	prompt := buildCompletionPrompt(req.Messages, req.BotName, req.TargetMessageID, o.completionSpeaker, o.completionPromptChars, o.completionBotLines, o.completionMediaTags)
	for attempt := 0; attempt < 2; attempt++ {
		generated, err := o.generateCompletion(ctx, req, prompt, maxTokens)
		if err != nil {
			return nil, err
		}
		raw := generated.Response
		if generated.DoneReason == "length" {
			if trimmed, ok := trimCompletionSentence(raw, completionMinSentenceRunes); ok {
				raw = trimmed
			} else if attempt == 0 {
				continue
			} else {
				raw = cutCompletionWordBoundary(strings.TrimSpace(raw))
			}
		}
		model := generated.Model
		if model == "" {
			model = req.Model
		}
		finishReason := generated.DoneReason
		if finishReason == "" {
			finishReason = "stop"
		}
		cleaned := cleanCompletionReply(raw, req.BotName)
		var mediaURL string
		if o.mediaResolver != nil && (cleaned != "" || attempt == 1 || len(ParseMediaTags(raw)) > 0) {
			if resolved, ok := o.mediaResolver.Resolve(ctx, MediaResolveRequest{
				RawReply:        raw,
				GuildID:         req.GuildID,
				ChannelID:       req.ChannelID,
				BotID:           req.BotID,
				TargetText:      targetV1Text,
				TargetMessageID: req.TargetMessageID,
			}); ok {
				mediaURL = resolved.URL
			}
		}
		content := AssembleMediaReply(cleaned, mediaURL)
		response := &ChatResponse{
			Content: content, MediaURL: mediaURL, Model: model,
			TokensUsed:   generated.EvalCount + generated.PromptEvalCount,
			PromptTokens: generated.PromptEvalCount, CompletionTokens: generated.EvalCount,
			FinishReason: finishReason, EmptyCompletion: content == "" && attempt == 1,
		}
		if content != "" || attempt == 1 {
			return response, nil
		}
	}
	return &ChatResponse{}, nil
}

func (o *OllamaProvider) generateCompletion(ctx context.Context, req *ChatRequest, prompt string, maxTokens int) (*ollamaGenerateResponse, error) {
	options := map[string]any{"num_predict": maxTokens, "stop": []string{"\n"}}
	if req.Temperature != 0 {
		options["temperature"] = req.Temperature
	}
	body, err := json.Marshal(map[string]any{"model": req.Model, "prompt": prompt, "raw": true, "stream": false, "options": options})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL.JoinPath("/api/generate").String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	var generated ollamaGenerateResponse
	if err := json.Unmarshal(responseBody, &generated); err != nil {
		if httpResp.StatusCode >= http.StatusBadRequest {
			return nil, &ProviderHTTPError{StatusCode: httpResp.StatusCode, Message: errorBodySnippet(responseBody)}
		}
		return nil, fmt.Errorf("unmarshal Ollama generate response: %w", err)
	}
	if generated.Error != "" {
		return nil, &ProviderHTTPError{StatusCode: httpResp.StatusCode, Message: generated.Error}
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return nil, &ProviderHTTPError{StatusCode: httpResp.StatusCode}
	}
	return &generated, nil
}

// buildCompletionPrompt renders the most recent messages as training-format
// chat lines that fit the model's context, then the bot's speaker name. There
// is no space after the colon: the tokenizer attaches it to the next word.
func buildCompletionPrompt(messages []Message, botName, targetMessageID, completionSpeaker string, promptChars, botLines int, mediaTags string) string {
	names := completionNames(messages)
	if botName == "" {
		botName = "bot"
	}
	if completionSpeaker == "" {
		completionSpeaker = botName
	}
	if promptChars <= 0 {
		promptChars = completionPromptChars
	}
	if botLines <= 0 {
		botLines = completionDefaultBotLines
	}
	ordered := messages
	if targetMessageID != "" {
		ordered = make([]Message, 0, len(messages))
		for _, msg := range messages {
			if msg.MessageID != targetMessageID {
				ordered = append(ordered, msg)
			}
		}
		for _, msg := range messages {
			if msg.MessageID == targetMessageID {
				ordered = append(ordered, msg)
				break
			}
		}
	}

	var lines []string
	size := 0
	botLineCount := 0
	for idx := len(ordered) - 1; idx >= 0; idx-- {
		msg := ordered[idx]
		if msg.AuthorBot || msg.CommandOutput {
			continue
		}
		if msg.Role != RoleUser && msg.Role != RoleAssistant {
			continue
		}
		speaker := msg.AuthorNick
		if msg.Role == RoleAssistant {
			if botLineCount >= botLines || IsErrorReply(msg.Content) {
				continue
			}
			speaker = completionSpeaker
		}
		if speaker == "" {
			continue
		}
		isTarget := targetMessageID != "" && msg.MessageID == targetMessageID
		text := completionText(msg, names, mediaTags)
		if msg.Role == RoleAssistant {
			text = limitCompletionBotLine(text, completionBotLineChars)
		}
		if text == "" && !isTarget {
			continue
		}
		line := speaker + ": " + text
		line = normalizeCompletionQuotes(line)
		if size+len(line) > promptChars && len(lines) > 0 && !isTarget {
			break
		}
		lines = append(lines, line)
		size += len(line) + 1
		if msg.Role == RoleAssistant {
			botLineCount++
		}
	}

	var prompt strings.Builder
	for idx := len(lines) - 1; idx >= 0; idx-- {
		prompt.WriteString(lines[idx])
		prompt.WriteString("\n")
	}
	prompt.WriteString(completionSpeaker)
	prompt.WriteString(":")
	return prompt.String()
}

func normalizeCompletionQuotes(text string) string {
	return strings.NewReplacer("‘", "'", "’", "'", "ʼ", "'", "‛", "'", "“", "\"", "”", "\"", "„", "\"", "‟", "\"").Replace(text)
}

// IsErrorReply reports whether text is one of the bot's own error replies.
func IsErrorReply(text string) bool {
	for _, prefix := range []string{"Désolé, j'ai des soucis techniques là", "Désolé, tous mes systèmes sont en rade", "Euh... j'ai perdu mes mots là"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// completionText returns a message's own text as one training-format line:
// mentions become @name, custom emojis :name:, and media is rendered with the
// media-tag renderer for the configured contract version. It reads Message.Text
// so context prefixes and image descriptions never leak into the prompt.
func completionText(msg Message, names map[string]string, mediaTags string) string {
	text := msg.Text
	text = completionUserMention.ReplaceAllStringFunc(text, func(mention string) string {
		if name, ok := names[completionUserMention.FindStringSubmatch(mention)[1]]; ok {
			return "@" + name
		}
		return ""
	})
	text = completionDiscordEmoji.ReplaceAllString(text, ":$1:")

	rendered := msg
	rendered.Content = strings.Join(strings.Fields(text), " ")
	text = RenderMediaMessage(rendered, mediaTags)
	return limitCompletionLine(text, completionLineChars)
}

func isSentenceEnd(r rune) bool {
	return r == '.' || r == '!' || r == '?' || r == '…'
}

// trimCompletionSentence trims text to the end of its last complete sentence
// (`.`, `!`, `?`, or `…`, including runs like `...` or `?!`, followed by
// whitespace or end of text), dropping any trailing unterminated media tag
// first. It returns ok=false when fewer than minRunes remain.
func trimCompletionSentence(text string, minRunes int) (string, bool) {
	s := strings.TrimSpace(text)
	if open := strings.LastIndex(s, "["); open > strings.LastIndex(s, "]") {
		s = strings.TrimSpace(s[:open])
	}
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		if !isSentenceEnd(runes[i]) {
			continue
		}
		if i < len(runes)-1 && !unicode.IsSpace(runes[i+1]) {
			continue
		}
		if i+1 < minRunes {
			return "", false
		}
		return string(runes[:i+1]), true
	}
	return "", false
}

func cutCompletionWordBoundary(cut string) string {
	if idx := strings.LastIndex(cut, " "); idx >= 0 {
		cut = cut[:idx]
	}
	if open := strings.LastIndex(cut, "["); open > strings.LastIndex(cut, "]") {
		cut = cut[:open]
	}
	return strings.TrimSpace(cut)
}

// limitCompletionLine caps a prompt line at maxRunes code points, cuts at the
// last space, and drops a tag that would be cut in the middle.
func limitCompletionLine(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return cutCompletionWordBoundary(string(runes[:maxRunes]))
}

// limitCompletionBotLine caps a bot prompt line at maxRunes code points,
// preferring the last complete sentence within maxRunes and otherwise
// falling back to limitCompletionLine.
func limitCompletionBotLine(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	prefix := string(runes[:maxRunes])
	if !unicode.IsSpace(runes[maxRunes]) {
		end := maxRunes
		for end > 0 && isSentenceEnd(runes[end-1]) {
			end--
		}
		prefix = string(runes[:end])
	}
	if trimmed, ok := trimCompletionSentence(prefix, completionMinSentenceRunes); ok {
		return trimmed
	}
	return limitCompletionLine(text, maxRunes)
}

func completionNames(messages []Message) map[string]string {
	names := make(map[string]string)
	for _, msg := range messages {
		if msg.AuthorID != "" && msg.AuthorNick != "" {
			names[msg.AuthorID] = msg.AuthorNick
		}
	}
	return names
}

func completionTargetMessage(messages []Message, targetMessageID string) (Message, bool) {
	if targetMessageID == "" {
		return Message{}, false
	}
	for _, msg := range messages {
		if msg.MessageID == targetMessageID {
			return msg, true
		}
	}
	return Message{}, false
}

func (o *OllamaProvider) emptyCompletion(req *ChatRequest) *ChatResponse {
	return &ChatResponse{
		Provider:        o.Name(),
		Model:           req.Model,
		FinishReason:    FinishReasonStop,
		EmptyCompletion: true,
	}
}

// completionTargetIsBot reports whether the message completion was asked to
// answer was written by another bot or a webhook.
func completionTargetIsBot(messages []Message, targetMessageID string) bool {
	target, ok := completionTargetMessage(messages, targetMessageID)
	return ok && target.AuthorBot
}

func completionTargetV1Text(messages []Message, targetMessageID string) string {
	renderPlainV1 := func(msg Message) string {
		rendered := msg
		rendered.Content = msg.Text
		return strings.TrimSpace(RenderMediaMessage(rendered, MediaTagsV1))
	}
	if targetMessageID != "" {
		if target, ok := completionTargetMessage(messages, targetMessageID); ok {
			return renderPlainV1(target)
		}
		return ""
	}
	for idx := len(messages) - 1; idx >= 0; idx-- {
		msg := messages[idx]
		if msg.Role == RoleUser && !msg.AuthorBot {
			if rendered := renderPlainV1(msg); rendered != "" {
				return rendered
			}
		}
	}
	return ""
}

// cleanCompletionReply drops what the bot should not post as text: a leading
// "name: " speaker, @mentions (the model learned them from the chat) and URL
// placeholders, and turns the speaker tag back into the bot's name.
func cleanCompletionReply(reply, botName string) string {
	trimmed := strings.TrimSpace(reply)
	// Avoid mistaking a leading media tag like "[gif: cat]" for a "speaker: " prefix.
	if loc := completionPlaceholder.FindStringIndex(trimmed); loc == nil || loc[0] > 0 {
		trimmed = completionLeadingSpeaker.ReplaceAllString(trimmed, "")
	}
	if botName == "" {
		botName = "bot"
	}
	trimmed = strings.ReplaceAll(trimmed, "§bot", botName)
	trimmed = completionReplyMention.ReplaceAllString(trimmed, "")
	trimmed = completionPlaceholder.ReplaceAllString(trimmed, "")
	return strings.Join(strings.Fields(trimmed), " ")
}
