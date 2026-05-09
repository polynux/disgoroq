package ai

import "context"

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
	Model        string         // e.g., "llama-3-70b", "dolphin3"
	SystemPrompt string         // System instructions
	Messages     []Message      // Conversation history
	Temperature  float32        // 0.0-1.0
	MaxTokens    int            // Max response tokens
	Images       []ImageContext // Images referenced in messages
}

// Message represents a single message in the conversation
type Message struct {
	Role       string // "user", "assistant", "system"
	Content    string // Text content
	AuthorID   string // Discord user ID
	AuthorNick string // Display name
	MessageID  string // Discord message ID
	ImageRefs  []int  // Indices into ChatRequest.Images
}

// ImageContext contains image metadata for vision processing
type ImageContext struct {
	MessageID string // Which Discord message has this image
	URL       string // Image URL
	Type      string // image/jpeg, image/png, etc.
	Width     int    // Image width in pixels
	Height    int    // Image height in pixels
	Size      int64  // File size in bytes
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
	Content      string // Generated text
	Provider     string // Provider that generated the response
	Model        string // Model that generated the response
	TokensUsed   int    // Total tokens consumed
	FinishReason string // "stop", "length", etc.
}

// VisionResponse contains the image description
type VisionResponse struct {
	Description  string // Generated description
	Provider     string // Provider that generated the response
	Model        string // Model used
	TokensUsed   int    // Tokens consumed
	FinishReason string // "stop", "length", etc.
}

// ModelInfo describes a model's capabilities
type ModelInfo struct {
	Name         string   // Model name
	Provider     string   // Provider name
	Capabilities []string // "chat", "vision", "streaming"
}

// Capability constants
const (
	CapabilityChat      = "chat"
	CapabilityVision    = "vision"
	CapabilityStreaming = "streaming"
)
