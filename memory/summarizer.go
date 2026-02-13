package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AIService defines the interface for AI text generation (matches existing DisgoroQ AI service)
type AIService interface {
	Chat(ctx context.Context, messages []Message, model string) (string, error)
}

// Message represents a message for AI chat (matches existing DisgoroQ message format)
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Summarizer handles AI-powered conversation summarization
type Summarizer struct {
	aiService AIService
	model     string
}

// NewSummarizer creates a new summarizer instance
func NewSummarizer(aiService AIService, model string) *Summarizer {
	if model == "" {
		model = "llama3-8b-8192" // Default model for summarization
	}
	return &Summarizer{
		aiService: aiService,
		model:     model,
	}
}

// HealthCheck verifies the summarizer is working correctly
func (s *Summarizer) HealthCheck(ctx context.Context) error {
	// Test with a simple prompt to verify the AI service is responsive
	testMessages := []Message{
		{Role: "user", Content: "Test connection"},
	}

	_, err := s.aiService.Chat(ctx, testMessages, s.model)
	if err != nil {
		return fmt.Errorf("summarizer health check failed: %w", err)
	}

	return nil
}

// Summarize generates or updates a conversation summary
func (s *Summarizer) Summarize(ctx context.Context, request *SummarizationRequest) (*SummarizationResult, error) {
	if len(request.Messages) == 0 {
		return &SummarizationResult{
			Success: false,
			Error:   fmt.Errorf("no messages to summarize"),
		}, nil
	}

	prompt := s.buildPrompt(request)

	messages := []Message{
		{
			Role:    "system",
			Content: "You are a conversation summarizer. Create concise, informative summaries that capture key topics, user interests, and important context. Focus on the main themes and user personality traits. Be brief but comprehensive.",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	summaryText, err := s.aiService.Chat(ctx, messages, s.model)
	if err != nil {
		return &SummarizationResult{
			Success: false,
			Error:   fmt.Errorf("AI service error: %w", err),
		}, nil
	}

	// Clean and validate the summary
	summaryText = s.cleanSummary(summaryText)
	if summaryText == "" {
		return &SummarizationResult{
			Success: false,
			Error:   fmt.Errorf("AI returned empty summary"),
		}, nil
	}

	// Create the new summary
	newSummary := &Summary{
		GuildID:        request.GuildID,
		UserID:         request.UserID,
		SummaryText:    summaryText,
		MessageCount:   int64(request.NewMessageCount),
		StartMessageID: request.Messages[0].MessageID,
		EndMessageID:   request.Messages[len(request.Messages)-1].MessageID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Embedding:      nil, // Will be set by embedding provider
	}

	// If we have a previous summary, this is an update
	if request.PreviousSummary != nil {
		newSummary.ID = request.PreviousSummary.ID
		newSummary.CreatedAt = request.PreviousSummary.CreatedAt
		newSummary.MessageCount = request.PreviousSummary.MessageCount + int64(request.NewMessageCount)
		newSummary.StartMessageID = request.PreviousSummary.StartMessageID
	}

	return &SummarizationResult{
		Summary:      newSummary,
		OldSummaryID: nil,
		Success:      true,
		Error:        nil,
	}, nil
}

// buildPrompt constructs the AI prompt for summarization
func (s *Summarizer) buildPrompt(request *SummarizationRequest) string {
	var prompt strings.Builder

	prompt.WriteString("Create a brief summary of this conversation (2-3 sentences max).")
	prompt.WriteString("\n\nIMPORTANT RULES:")
	prompt.WriteString("\n- Respond ONLY with the summary text")
	prompt.WriteString("\n- NO introductions like 'Here's a summary' or 'Summary:'")
	prompt.WriteString("\n- NO formatting markers like '**Summary:**'")
	prompt.WriteString("\n- Use the SAME LANGUAGE as the conversation")
	prompt.WriteString("\n- Focus on: topics, user interests, key info, communication style")
	prompt.WriteString("\n\nCONVERSATION:\n")

	// Add previous summary if available
	if request.PreviousSummary != nil {
		prompt.WriteString("\nPREVIOUS CONTEXT: ")
		prompt.WriteString(request.PreviousSummary.SummaryText)
		prompt.WriteString("\n\nNEW MESSAGES:\n")
	}

	// Add the new messages
	for i, msg := range request.Messages {
		prompt.WriteString(fmt.Sprintf("[%d] %s: %s\n", i+1, msg.AuthorNick, msg.Content))
	}

	prompt.WriteString("\nYOUR SUMMARY (direct output, no prefix):")

	return prompt.String()
}

func (s *Summarizer) cleanSummary(summary string) string {
	// List of prefixes to remove (case-insensitive variations)
	prefixes := []string{
		"Summary:",
		"**Summary:**",
		"Here's a summary:",
		"Here's a summary",
		"Here is a summary:",
		"Here is a summary",
		"Based on the conversation:",
		"Based on the conversation",
		"Based on this conversation:",
		"Based on this conversation",
		"The summary is:",
		"The summary is",
		"A summary:",
		"Summary of the conversation:",
		"Summary of the conversation",
		"Here's what was discussed:",
		"Here's what was discussed",
		"In summary:",
		"In summary",
	}

	// Try removing each prefix (case-insensitive)
	summary = strings.TrimSpace(summary)
	for _, prefix := range prefixes {
		if strings.HasPrefix(strings.ToLower(summary), strings.ToLower(prefix)) {
			summary = strings.TrimPrefix(summary, prefix)
			summary = strings.TrimPrefix(summary, ":")
			summary = strings.TrimSpace(summary)
			break
		}
	}

	// Remove markdown bold markers
	summary = strings.TrimPrefix(summary, "**")
	summary = strings.TrimSuffix(summary, "**")
	summary = strings.TrimSpace(summary)

	maxLength := 500
	if len(summary) > maxLength {
		lastSentence := strings.LastIndex(summary[:maxLength], ".")
		if lastSentence > maxLength/2 {
			summary = summary[:lastSentence+1]
		} else {
			summary = summary[:maxLength] + "..."
		}
	}

	return summary
}

func (s *Summarizer) ValidateSummary(summary string) error {
	if summary == "" {
		return fmt.Errorf("summary is empty")
	}

	if len(summary) < 20 {
		return fmt.Errorf("summary too short: %d characters", len(summary))
	}

	if len(summary) > 500 {
		return fmt.Errorf("summary too long: %d characters", len(summary))
	}

	words := strings.Fields(strings.ToLower(summary))
	if len(words) == 0 {
		return fmt.Errorf("summary contains no words")
	}

	wordCount := make(map[string]int)
	for _, word := range words {
		wordCount[word]++
	}

	repeatedWords := 0
	for _, count := range wordCount {
		if count > 1 {
			repeatedWords += count - 1
		}
	}

	if float64(repeatedWords) > float64(len(words))*0.5 {
		return fmt.Errorf("summary contains excessive repetition")
	}

	return nil
}

func (s *Summarizer) EstimateSummaryQuality(summary string) int {
	if summary == "" {
		return 0
	}

	score := 50

	length := len(summary)
	switch {
	case length < 50:
		score -= 20
	case length < 100:
		score += 10
	case length < 300:
		score += 20
	case length < 400:
		score += 10
	default:
		score -= 10
	}

	words := strings.Fields(strings.ToLower(summary))
	uniqueWords := make(map[string]bool)
	for _, word := range words {
		uniqueWords[word] = true
	}

	if len(words) > 0 {
		diversity := float64(len(uniqueWords)) / float64(len(words))
		if diversity > 0.8 {
			score += 15
		} else if diversity > 0.6 {
			score += 10
		} else if diversity < 0.4 {
			score -= 15
		}
	}

	topicWords := []string{"discussed", "mentioned", "talked", "said", "asked", "explained", "shared", "suggested"}
	topicScore := 0
	for _, word := range topicWords {
		if strings.Contains(strings.ToLower(summary), word) {
			topicScore++
		}
	}
	score += topicScore * 3

	sentences := strings.Split(summary, ".")
	if len(sentences) >= 2 && len(sentences) <= 5 {
		score += 5
	}

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	return score
}

func (s *Summarizer) ExtractKeyTopics(messages []BufferedMessage) []string {
	keywords := make(map[string]int)

	ignoreWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "up": true, "about": true, "into": true,
		"through": true, "during": true, "before": true, "after": true, "above": true,
		"below": true, "between": true, "among": true, "is": true, "are": true, "was": true,
		"were": true, "be": true, "been": true, "being": true, "have": true, "has": true,
		"had": true, "do": true, "does": true, "did": true, "will": true, "would": true,
		"could": true, "should": true, "may": true, "might": true, "must": true, "can": true,
		"i": true, "you": true, "he": true, "she": true, "it": true, "we": true, "they": true,
		"me": true, "him": true, "her": true, "us": true, "them": true, "my": true, "your": true,
		"his": true, "its": true, "our": true, "their": true, "this": true, "that": true,
		"these": true, "those": true, "here": true, "there": true, "now": true, "then": true,
	}

	for _, msg := range messages {
		words := strings.Fields(strings.ToLower(msg.Content))
		for _, word := range words {
			word = strings.Trim(word, ".,!?;:")
			if len(word) < 3 || ignoreWords[word] {
				continue
			}
			keywords[word]++
		}
	}

	type keywordFreq struct {
		word  string
		count int
	}

	var keywordList []keywordFreq
	for word, count := range keywords {
		keywordList = append(keywordList, keywordFreq{word: word, count: count})
	}

	for i := 0; i < len(keywordList); i++ {
		for j := i + 1; j < len(keywordList); j++ {
			if keywordList[j].count > keywordList[i].count {
				keywordList[i], keywordList[j] = keywordList[j], keywordList[i]
			}
		}
	}

	var result []string
	maxKeywords := 10
	if len(keywordList) < maxKeywords {
		maxKeywords = len(keywordList)
	}

	for i := 0; i < maxKeywords; i++ {
		result = append(result, keywordList[i].word)
	}

	return result
}

// FormatMessagesForPrompt formats buffered messages for inclusion in AI prompts
func (s *Summarizer) FormatMessagesForPrompt(messages []BufferedMessage) string {
	var formatted strings.Builder

	for i, msg := range messages {
		timestamp := msg.Timestamp.Format("15:04")
		formatted.WriteString(fmt.Sprintf("[%s] %s: %s", timestamp, msg.AuthorNick, msg.Content))
		if i < len(messages)-1 {
			formatted.WriteString("\n")
		}
	}

	return formatted.String()
}

// IsSummaryStale checks if a summary is outdated based on its age
func (s *Summarizer) IsSummaryStale(summary *Summary, maxAge time.Duration) bool {
	if summary == nil {
		return true
	}
	return time.Since(summary.UpdatedAt) > maxAge
}

func (s *Summarizer) MergeSummaries(summaries []Summary) (string, error) {
	if len(summaries) == 0 {
		return "", fmt.Errorf("no summaries to merge")
	}

	if len(summaries) == 1 {
		return summaries[0].SummaryText, nil
	}

	var combined strings.Builder
	combined.WriteString("Combined conversation summary:\n\n")

	for i, summary := range summaries {
		combined.WriteString(fmt.Sprintf("Period %d: %s\n", i+1, summary.SummaryText))
		if i < len(summaries)-1 {
			combined.WriteString("\n")
		}
	}

	return combined.String(), nil
}
