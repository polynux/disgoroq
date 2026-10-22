package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Completion mode serves raw chat-log models (e.g. luciole-1b-discord1 from
// discord-ia-waldo): models trained on plain "name: text" Discord lines, not on
// instructions. They cannot follow a system prompt or call tools, so the
// request becomes the recent chat as lines ending with the bot's speaker tag
// ("§bot:" for Luciole), sent to /api/generate with raw=true, and the reply is
// that one line.

const (
	// Luciole has a 1,024-token context; French chat averages about 2.8 chars/token.
	completionPromptChars = 2400
	completionLineChars   = 300
	completionMaxTokens   = 64
)

var (
	completionUserMention    = regexp.MustCompile(`<@!?(\d+)>`)
	completionDiscordEmoji   = regexp.MustCompile(`<a?:(\w+):\d+>`)
	completionURL            = regexp.MustCompile(`(?i)(?:https?://|www\.)\S+`)
	completionPlaceholder    = regexp.MustCompile(`\[(?:gif|lien|image|youtube|tweet|tiktok|insta|son|steam)\]`)
	completionReplyMention   = regexp.MustCompile(`@\S+`)
	completionLeadingSpeaker = regexp.MustCompile(`^[^:\s]{1,32}:(?:\s|$)`)
	// Same URL placeholders as the training data (discord-ia-waldo prep/discord.py).
	completionURLKinds = []struct {
		pattern     *regexp.Regexp
		placeholder string
	}{
		{regexp.MustCompile(`(?i)tenor\.com|giphy\.com|klipy\.com|\.gif\b`), "[gif]"},
		{regexp.MustCompile(`(?i)youtube\.com|youtu\.be`), "[youtube]"},
		{regexp.MustCompile(`(?i)twitter\.com|//x\.com|twimg\.com|fxtwitter|vxtwitter|fixupx`), "[tweet]"},
		{regexp.MustCompile(`(?i)tiktok\.com|tnktok\.com`), "[tiktok]"},
		{regexp.MustCompile(`(?i)instagram\.com`), "[insta]"},
		{regexp.MustCompile(`(?i)soundcloud\.com|spotify\.com|deezer\.com`), "[son]"},
		{regexp.MustCompile(`(?i)steampowered\.com|steamcommunity\.com`), "[steam]"},
		{regexp.MustCompile(`(?i)cdn\.discordapp\.com|media\.discordapp\.net`), "[image]"},
	}
)

type ollamaGenerateResponse struct {
	Model           string `json:"model"`
	Response        string `json:"response"`
	Error           string `json:"error,omitempty"`
	DoneReason      string `json:"done_reason,omitempty"`
	EvalCount       int    `json:"eval_count,omitempty"`
	PromptEvalCount int    `json:"prompt_eval_count,omitempty"`
}

// SetCompletionMode switches chat requests to raw chat-log completion.
func (o *OllamaProvider) SetCompletionMode(enabled bool) {
	o.completion = enabled
}

func (o *OllamaProvider) SetCompletionConfig(speaker string, promptChars int) {
	o.completionSpeaker = speaker
	if promptChars > 0 {
		o.completionPromptChars = promptChars
	}
}

func (o *OllamaProvider) completionChat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 || maxTokens > completionMaxTokens {
		maxTokens = completionMaxTokens
	}
	prompt := buildCompletionPrompt(req.Messages, req.BotName, req.TargetMessageID, o.completionSpeaker, o.completionPromptChars)
	for attempt := 0; attempt < 2; attempt++ {
		generated, err := o.generateCompletion(ctx, req, prompt, maxTokens)
		if err != nil {
			return nil, err
		}
		model := generated.Model
		if model == "" {
			model = req.Model
		}
		finishReason := generated.DoneReason
		if finishReason == "" {
			finishReason = "stop"
		}
		content := cleanCompletionReply(generated.Response, req.BotName)
		response := &ChatResponse{
			Content: content, Model: model,
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
		return nil, fmt.Errorf("unmarshal Ollama generate response: %w", err)
	}
	if generated.Error != "" {
		return nil, errors.New(generated.Error)
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("ollama generate failed: %s", httpResp.Status)
	}
	return &generated, nil
}

// buildCompletionPrompt renders the most recent messages as training-format
// chat lines that fit the model's context, then the bot's speaker name. There
// is no space after the colon: the tokenizer attaches it to the next word.
func buildCompletionPrompt(messages []Message, botName, targetMessageID, completionSpeaker string, promptChars int) string {
	names := make(map[string]string)
	for _, msg := range messages {
		if msg.AuthorID != "" && msg.AuthorNick != "" {
			names[msg.AuthorID] = msg.AuthorNick
		}
	}
	if botName == "" {
		botName = "bot"
	}
	if completionSpeaker == "" {
		completionSpeaker = botName
	}
	if promptChars <= 0 {
		promptChars = completionPromptChars
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
	botLines := 0
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
			if botLines >= 2 || isCompletionError(msg.Content) {
				continue
			}
			speaker = completionSpeaker
		}
		if speaker == "" {
			continue
		}
		text := completionText(msg, names)
		if text == "" {
			continue
		}
		line := speaker + ": " + text
		line = normalizeCompletionQuotes(line)
		isTarget := targetMessageID != "" && msg.MessageID == targetMessageID
		if size+len(line) > promptChars && len(lines) > 0 && !isTarget {
			break
		}
		lines = append(lines, line)
		size += len(line) + 1
		if msg.Role == RoleAssistant {
			botLines++
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

func isCompletionError(text string) bool {
	for _, prefix := range []string{"Désolé, j'ai des soucis techniques là", "Désolé, tous mes systèmes sont en rade", "Euh... j'ai perdu mes mots là"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// completionText returns a message's text as one training-format line: user
// messages lose the context builder's "<@id>nick: " prefix, mentions become
// @name, custom emojis :name:, URLs placeholders.
func completionText(msg Message, names map[string]string) string {
	text := msg.Content
	if msg.Role == RoleUser {
		text = strings.TrimPrefix(text, "<@"+msg.AuthorID+">"+msg.AuthorNick+": ")
		if strings.HasPrefix(text, "[Document Summary]") {
			return ""
		}
	}
	text = completionUserMention.ReplaceAllStringFunc(text, func(mention string) string {
		if name, ok := names[completionUserMention.FindStringSubmatch(mention)[1]]; ok {
			return "@" + name
		}
		return ""
	})
	text = completionDiscordEmoji.ReplaceAllString(text, ":$1:")
	text = completionURL.ReplaceAllStringFunc(text, func(url string) string {
		for _, kind := range completionURLKinds {
			if kind.pattern.MatchString(url) {
				return kind.placeholder
			}
		}
		return "[lien]"
	})
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > completionLineChars {
		text = strings.ToValidUTF8(text[:completionLineChars], "")
	}
	return text
}

// cleanCompletionReply drops what the bot should not post as text: a leading
// "name: " speaker, @mentions (the model learned them from the chat) and URL
// placeholders, and turns the speaker tag back into the bot's name.
func cleanCompletionReply(reply, botName string) string {
	reply = completionLeadingSpeaker.ReplaceAllString(strings.TrimSpace(reply), "")
	if botName == "" {
		botName = "bot"
	}
	reply = strings.ReplaceAll(reply, "§bot", botName)
	reply = completionReplyMention.ReplaceAllString(reply, "")
	reply = completionPlaceholder.ReplaceAllString(reply, "")
	return strings.Join(strings.Fields(reply), " ")
}
