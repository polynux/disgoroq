package ai

import "context"

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"

	ToolTypeFunction = "function"

	ToolChoiceAuto     = "auto"
	ToolChoiceNone     = "none"
	ToolChoiceRequired = "required"

	FinishReasonStop      = "stop"
	FinishReasonLength    = "length"
	FinishReasonToolCalls = "tool_calls"
	FinishReasonCache     = "cache"
)

// Provider defines the interface for AI providers (GROQ, Ollama, etc.)
type Provider interface {
	// Chat generates text responses with full Discord message context
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// Vision analyzes images with text context
	Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error)

	// Name returns the provider name (groq, ollama, etc.)
	Name() string

	// AvailableModels returns supported models for this provider
	AvailableModels() []ModelInfo
}

// ChatRequest contains all context needed for AI text generation
type ChatRequest struct {
	Model           string                // e.g., "llama-3-70b", "dolphin3"
	SystemPrompt    string                // System instructions
	Messages        []Message             // Conversation history
	Tools           []ToolDefinition      // Optional function tools available to the model
	ToolChoice      *ToolChoice           // Optional tool-calling policy override
	Temperature     float32               // 0.0-1.0
	MaxTokens       int                   // Max response tokens
	Images          []ImageContext        // Images referenced in messages
	AttachmentCache *AttachmentCacheInput // Optional detached attachment summary cache key
}

// Message represents a single message in the conversation
type Message struct {
	Role       string     // "user", "assistant", "system", "tool"
	Content    string     // Text content
	AuthorID   string     // Discord user ID
	AuthorNick string     // Display name
	MessageID  string     // Discord message ID
	Name       string     // Optional tool/function name
	ToolCallID string     // Links a tool result to an assistant tool call
	ToolCalls  []ToolCall // Assistant-requested tool calls
	ImageRefs  []int      // Indices into ChatRequest.Images
}

// ImageContext contains image metadata for vision processing
type ImageContext struct {
	MessageID string // Which Discord message has this image
	URL       string // Image URL or materialized data URI
	SourceURL string // Original attachment URL when materialized
	Type      string // image/jpeg, image/png, etc.
	Width     int    // Image width in pixels
	Height    int    // Image height in pixels
	Size      int64  // File size in bytes
}

// DocumentContext contains document metadata for summary processing.
type DocumentContext struct {
	MessageID   string // Which Discord message has this document
	URL         string // Document URL
	Filename    string // Original filename
	ContentType string // MIME type
	Size        int64  // File size in bytes
}

// AttachmentCacheInput identifies a detached attachment-to-text conversion that
// can be reused across requests.
type AttachmentCacheInput struct {
	Kind               string // image_description, document_summary, etc.
	AttachmentKey      string // Stable fingerprint for the effective attachment input
	SourceURL          string // Original attachment URL or data URI
	Filename           string // Original filename when applicable
	ContentType        string // MIME type
	SizeBytes          int64  // Attachment size in bytes
	InstructionVersion string // Prompt/instruction discriminator
}

// VisionRequest contains parameters for image analysis
type VisionRequest struct {
	Model       string // Vision model name
	Instruction string // What to extract from image
	ImageURL    string // Image to analyze
	ImageType   string // MIME type
	Width       int    // Image dimensions
	Height      int
	MaxTokens   int     // Max description length
	Temperature float32 // 0.0-1.0
}

// ChatResponse contains the AI-generated text response
type ChatResponse struct {
	Content          string // Generated text
	ToolCalls        []ToolCall
	Provider         string // Provider that generated the response
	Model            string // Model that generated the response
	TokensUsed       int    // Total tokens consumed
	PromptTokens     int    // Input prompt tokens when exposed by provider
	CompletionTokens int    // Output tokens when exposed by provider
	CachedTokens     int    // Prompt tokens served from cache when exposed
	CacheWriteTokens int    // Prompt tokens written to cache when exposed
	FinishReason     string // "stop", "length", etc.
}

// VisionResponse contains the image description
type VisionResponse struct {
	Description      string // Generated description
	Provider         string // Provider that generated the response
	Model            string // Model used
	TokensUsed       int    // Tokens consumed
	PromptTokens     int    // Input prompt tokens when exposed by provider
	CompletionTokens int    // Output tokens when exposed by provider
	CachedTokens     int    // Prompt tokens served from cache when exposed
	CacheWriteTokens int    // Prompt tokens written to cache when exposed
	FinishReason     string // "stop", "length", etc.
}

// ModelInfo describes a model's capabilities
type ModelInfo struct {
	Name         string   // Model name
	Provider     string   // Provider name
	Capabilities []string // "chat", "vision", "streaming"
}

// ToolChoice configures whether the model may call tools.
type ToolChoice struct {
	Mode string // auto, none, required
	Name string // Optional specific tool name
}

// ToolDefinition describes a model-callable function tool.
type ToolDefinition struct {
	Type     string                 // Defaults to ToolTypeFunction
	Function ToolFunctionDefinition // Function metadata and parameter schema
}

// ToolFunctionDefinition contains the metadata for a callable function tool.
type ToolFunctionDefinition struct {
	Name        string            // Stable tool name exposed to the model
	Description string            // Human-readable description for the model
	Parameters  ToolSchema        // JSON-schema-like parameter definition
	Strict      bool              // Optional provider hint for stricter validation
	Metadata    map[string]string // Optional internal metadata
}

// ToolSchema describes tool input parameters using a JSON-schema-like shape.
type ToolSchema struct {
	Type                 string                  `json:"type,omitempty"`
	Description          string                  `json:"description,omitempty"`
	Properties           map[string]ToolProperty `json:"properties,omitempty"`
	Required             []string                `json:"required,omitempty"`
	Enum                 []string                `json:"enum,omitempty"`
	Items                *ToolSchema             `json:"items,omitempty"`
	AdditionalProperties *bool                   `json:"additionalProperties,omitempty"`
}

// ToolProperty is an alias for ToolSchema to make property maps read naturally.
type ToolProperty = ToolSchema

// ToolCall captures a single assistant-requested function invocation.
type ToolCall struct {
	ID       string // Provider-generated identifier when available
	Type     string // Defaults to ToolTypeFunction
	Function ToolFunctionCall
}

// ToolFunctionCall contains the requested function name and raw JSON arguments.
type ToolFunctionCall struct {
	Name      string // Tool/function name
	Arguments string // JSON object encoded as a string
}

// EffectiveType returns the declared tool type or the default function type.
func (t ToolDefinition) EffectiveType() string {
	if t.Type == "" {
		return ToolTypeFunction
	}
	return t.Type
}

// EffectiveType returns the declared tool call type or the default function type.
func (t ToolCall) EffectiveType() string {
	if t.Type == "" {
		return ToolTypeFunction
	}
	return t.Type
}

// HasToolCalls reports whether the message contains assistant tool calls.
func (m Message) HasToolCalls() bool {
	return len(m.ToolCalls) > 0
}

// HasToolCalls reports whether the response requests tool execution.
func (r *ChatResponse) HasToolCalls() bool {
	return r != nil && len(r.ToolCalls) > 0
}

// AssistantToolCallMessage constructs an assistant message that requests tool execution.
func AssistantToolCallMessage(content string, toolCalls ...ToolCall) Message {
	return Message{
		Role:      RoleAssistant,
		Content:   content,
		ToolCalls: append([]ToolCall(nil), toolCalls...),
	}
}

// ToolResultMessage constructs a tool result message linked to a prior call.
func ToolResultMessage(toolCallID, name, content string) Message {
	return Message{
		Role:       RoleTool,
		Name:       name,
		ToolCallID: toolCallID,
		Content:    content,
	}
}

// Capability constants
const (
	CapabilityChat      = "chat"
	CapabilityVision    = "vision"
	CapabilityStreaming = "streaming"
)
