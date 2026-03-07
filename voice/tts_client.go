package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// TTSHTTPClient implements TTSClient for the OpenAI-compatible Qwen3-TTS service.
type TTSHTTPClient struct {
	endpoint     string
	model        string
	defaultVoice string
	sampleRate   int
	timeout      time.Duration
	httpClient   *http.Client
	modelLoaded  bool
	mu           sync.RWMutex

	// Voice cloning reference
	defaultRefAudio []byte
	defaultRefText  string
}

// TTSHTTPConfig contains configuration for the TTS HTTP client.
type TTSHTTPConfig struct {
	Endpoint        string
	Model           string
	DefaultVoice    string
	SampleRate      int
	TimeoutMs       int
	DefaultRefAudio string // Path to default reference audio file for voice cloning
	DefaultRefText  string // Default reference text transcript
}

// TTSGenerateRequest represents a TTS generation request.
type TTSGenerateRequest struct {
	Input          string                `json:"input"`
	ResponseFormat string                `json:"response_format,omitempty"`
	Speed          float64               `json:"speed,omitempty"`
	Language       string                `json:"language,omitempty"`
	Stream         bool                  `json:"stream,omitempty"`
	Normalization  *NormalizationOptions `json:"normalization_options,omitempty"`
}

// NormalizationOptions contains text normalization settings.
type NormalizationOptions struct {
	Normalize                 bool `json:"normalize"`
	UnitNormalization         bool `json:"unit_normalization"`
	URLNormalization          bool `json:"url_normalization"`
	EmailNormalization        bool `json:"email_normalization"`
	OptionalPluralizationNorm bool `json:"optional_pluralization_normalization"`
	PhoneNormalization        bool `json:"phone_normalization"`
	ReplaceRemainingSymbols   bool `json:"replace_remaining_symbols"`
}

// VoiceInfo represents voice information from the API.
type VoiceInfo struct {
	VoiceName       string   `json:"voice_name"`
	AvailableVoices []string `json:"available_voices"`
	Language        string   `json:"language"`
	Mode            string   `json:"mode"`
}

// VRAMResponse represents VRAM status.
type VRAMResponse struct {
	TotalMB     int64 `json:"total_mb"`
	UsedMB      int64 `json:"used_mb"`
	FreeMB      int64 `json:"free_mb"`
	ModelLoaded bool  `json:"model_loaded"`
}

// HealthResponse represents health check response.
type HealthResponse struct {
	Status  string `json:"status"`
	Backend struct {
		Name                 string `json:"name"`
		ModelID              string `json:"model_id"`
		Ready                bool   `json:"ready"`
		SupportsVoiceCloning bool   `json:"supports_voice_cloning"`
	} `json:"backend"`
	Device struct {
		Type         string `json:"type"`
		GPUAvailable bool   `json:"gpu_available"`
		GPUName      string `json:"gpu_name"`
		VRAMTotal    string `json:"vram_total"`
		VRAMUsed     string `json:"vram_used"`
	} `json:"device"`
}

// NewTTSHTTPClient creates a new TTS HTTP client.
func NewTTSHTTPClient(config TTSHTTPConfig) *TTSHTTPClient {
	client := &TTSHTTPClient{
		endpoint:     config.Endpoint,
		model:        config.Model,
		defaultVoice: config.DefaultVoice,
		sampleRate:   config.SampleRate,
		timeout:      time.Duration(config.TimeoutMs) * time.Millisecond,
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutMs) * time.Millisecond,
		},
		modelLoaded: false,
	}

	// Load default reference audio if provided
	if config.DefaultRefAudio != "" {
		// In production, load from file
		// For now, we'll handle this in the voice cloning logic
		client.defaultRefText = config.DefaultRefText
	}

	return client
}

// Stream generates audio from text using the pre-loaded custom voice.
// Uses the simplified /v1/tts/generate endpoint.
func (c *TTSHTTPClient) Stream(ctx context.Context, req *TTSRequest) (io.ReadCloser, error) {
	c.mu.RLock()
	endpoint := c.endpoint
	c.mu.RUnlock()

	// Build TTS request
	ttsReq := TTSGenerateRequest{
		Input:          req.Text,
		ResponseFormat: "wav",
		Speed:          1.0,
		Language:       "French",
		Normalization: &NormalizationOptions{
			Normalize:                 true,
			UnitNormalization:         true,
			URLNormalization:          true,
			EmailNormalization:        true,
			OptionalPluralizationNorm: true,
			PhoneNormalization:        true,
			ReplaceRemainingSymbols:   true,
		},
	}

	jsonBody, err := json.Marshal(ttsReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		endpoint+"/v1/tts/generate", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("TTS request failed", zap.Error(err))
		return nil, fmt.Errorf("TTS request failed: %w", err)
	}

	// Check for errors
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("TTS service error",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(bodyBytes)))
		return nil, fmt.Errorf("TTS service returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	c.mu.Lock()
	c.modelLoaded = true
	c.mu.Unlock()

	logger.Debug("TTS stream started",
		zap.String("text", truncateText(req.Text, 50)))

	return resp.Body, nil
}

// StreamStreaming generates audio with real-time streaming chunks.
// It returns a channel that yields audio chunks as they arrive from the server.
// This is significantly faster than waiting for complete generation because
// the first audio starts playing within ~400-800ms instead of waiting for
// the entire response to be generated.
func (c *TTSHTTPClient) StreamStreaming(ctx context.Context, req *TTSRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 10)

	// Build TTS request with streaming enabled
	ttsReq := TTSGenerateRequest{
		Input:          req.Text,
		ResponseFormat: "pcm", // PCM is best for streaming (no encoding overhead)
		Speed:          1.0,
		Language:       "French",
		Stream:         true, // Enable streaming on server
		Normalization: &NormalizationOptions{
			Normalize:                 true,
			UnitNormalization:         true,
			URLNormalization:          true,
			EmailNormalization:        true,
			OptionalPluralizationNorm: true,
			PhoneNormalization:        true,
			ReplaceRemainingSymbols:   true,
		},
	}

	jsonBody, err := json.Marshal(ttsReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	c.mu.RLock()
	endpoint := c.endpoint
	c.mu.RUnlock()

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		endpoint+"/v1/tts/generate", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send request with longer timeout for streaming
	httpClient := &http.Client{
		Timeout: 120 * time.Second, // Long timeout for streaming
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		logger.Error("TTS streaming request failed", zap.Error(err))
		return nil, fmt.Errorf("TTS streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TTS service returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	c.mu.Lock()
	c.modelLoaded = true
	c.mu.Unlock()

	logger.Info("TTS streaming started",
		zap.String("text", truncateText(req.Text, 50)))

	// Start goroutine to read chunks
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// Read PCM chunks from response body
		// Each chunk is approximately 200-400ms of audio
		buf := make([]byte, 8192) // ~170ms of 24kHz mono audio
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				ch <- StreamChunk{
					Data:       chunk,
					SampleRate: c.sampleRate,
					Format:     "pcm",
				}
			}
			if err != nil {
				if err != io.EOF {
					logger.Error("TTS streaming read error", zap.Error(err))
					ch <- StreamChunk{Err: err}
				}
				return
			}
		}
	}()

	return ch, nil
}

// LoadModel loads the TTS model into VRAM.
func (c *TTSHTTPClient) LoadModel(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.modelLoaded {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/load", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 120 * time.Second} // Longer timeout for model loading
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to load model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("load model returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	c.modelLoaded = true
	logger.Info("TTS model loaded", zap.String("model", c.model))

	return nil
}

// UnloadModel unloads the TTS model from VRAM.
func (c *TTSHTTPClient) UnloadModel(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.modelLoaded {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/unload", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		logger.Warn("Failed to unload model", zap.Error(err))
		return fmt.Errorf("failed to unload model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unload model returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	c.modelLoaded = false
	logger.Info("TTS model unloaded")

	return nil
}

// GetVRAM returns the available VRAM in MB.
func (c *TTSHTTPClient) GetVRAM(ctx context.Context) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/vram", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to get VRAM: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("VRAM endpoint returned status %d", resp.StatusCode)
	}

	var vramResp VRAMResponse
	if err := json.NewDecoder(resp.Body).Decode(&vramResp); err != nil {
		return 0, fmt.Errorf("failed to decode VRAM response: %w", err)
	}

	c.mu.Lock()
	c.modelLoaded = vramResp.ModelLoaded
	c.mu.Unlock()

	return vramResp.FreeMB, nil
}

// IsModelLoaded returns whether the TTS model is currently loaded.
func (c *TTSHTTPClient) IsModelLoaded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modelLoaded
}

// Close closes the connection to the TTS service.
func (c *TTSHTTPClient) Close() error {
	return nil
}

// Health checks if the TTS service is available.
func (c *TTSHTTPClient) Health(ctx context.Context) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create health request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("TTS health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TTS service unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// GetVoice returns information about the loaded voice.
func (c *TTSHTTPClient) GetVoice(ctx context.Context) (*VoiceInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/v1/tts/voice", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get voice: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voice endpoint returned status %d", resp.StatusCode)
	}

	var voiceInfo VoiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&voiceInfo); err != nil {
		return nil, fmt.Errorf("failed to decode voice response: %w", err)
	}

	return &voiceInfo, nil
}

// Generate generates complete audio for text and returns it as a byte slice.
// This is the recommended method for static (non-streaming) TTS.
func (c *TTSHTTPClient) Generate(ctx context.Context, req *TTSRequest) ([]byte, error) {
	audioStream, err := c.Stream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("TTS request failed: %w", err)
	}
	defer audioStream.Close()

	audio, err := io.ReadAll(audioStream)
	if err != nil {
		return nil, fmt.Errorf("failed to read TTS audio: %w", err)
	}

	logger.Debug("TTS audio generated",
		zap.String("text", truncateText(req.Text, 50)),
		zap.Int("bytes", len(audio)))

	return audio, nil
}

// MockTTSClient is a mock TTS client for testing.
type MockTTSClient struct {
	Audio       []byte
	Error       error
	VRAM        int64
	ModelLoaded bool
}

// Stream implements TTSClient.
func (m *MockTTSClient) Stream(ctx context.Context, req *TTSRequest) (io.ReadCloser, error) {
	if m.Error != nil {
		return nil, m.Error
	}
	m.ModelLoaded = true
	return io.NopCloser(bytes.NewReader(m.Audio)), nil
}

// StreamStreaming implements TTSClient.
func (m *MockTTSClient) StreamStreaming(ctx context.Context, req *TTSRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 1)
	if m.Error != nil {
		go func() {
			defer close(ch)
			ch <- StreamChunk{Err: m.Error}
		}()
		return ch, nil
	}
	m.ModelLoaded = true
	go func() {
		defer close(ch)
		// Send all audio in one chunk
		ch <- StreamChunk{
			Data:       m.Audio,
			SampleRate: 24000,
			Format:     "pcm",
		}
	}()
	return ch, nil
}

// Generate implements TTSClient.
func (m *MockTTSClient) Generate(ctx context.Context, req *TTSRequest) ([]byte, error) {
	if m.Error != nil {
		return nil, m.Error
	}
	m.ModelLoaded = true
	return m.Audio, nil
}

// LoadModel implements TTSClient.
func (m *MockTTSClient) LoadModel(ctx context.Context) error {
	m.ModelLoaded = true
	return m.Error
}

// UnloadModel implements TTSClient.
func (m *MockTTSClient) UnloadModel(ctx context.Context) error {
	m.ModelLoaded = false
	return m.Error
}

// GetVRAM implements TTSClient.
func (m *MockTTSClient) GetVRAM(ctx context.Context) (int64, error) {
	return m.VRAM, m.Error
}

// IsModelLoaded implements TTSClient.
func (m *MockTTSClient) IsModelLoaded() bool {
	return m.ModelLoaded
}

// Close implements TTSClient.
func (m *MockTTSClient) Close() error {
	return nil
}

// Helper functions

func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}

func readFile(path string) ([]byte, error) {
	// In production, read from filesystem
	// For now, return empty as reference audio should be pre-loaded
	return nil, fmt.Errorf("file reading not implemented - use pre-loaded voices")
}

// GenerateAndPlay combines TTS generation with audio playback through the voice manager.
func GenerateAndPlay(ctx context.Context, ttsClient TTSClient, manager *Manager, guildID, text string, voiceID string) error {
	req := &TTSRequest{
		Text:    text,
		VoiceID: voiceID,
	}

	// Use the static Generate method for simpler audio generation
	audio, err := ttsClient.Generate(ctx, req)
	if err != nil {
		return fmt.Errorf("TTS generation failed: %w", err)
	}

	// Play through voice manager
	if err := manager.PlayAudio(ctx, guildID, audio, 24000); err != nil {
		return fmt.Errorf("audio playback failed: %w", err)
	}

	return nil
}

// SplitForStreaming splits text into sentences for streaming TTS.
func SplitForStreaming(text string) []string {
	// Split on sentence boundaries
	sentences := strings.SplitAfter(text, ".")
	var result []string

	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}

		// Also split on question marks and exclamation points
		subParts := strings.SplitAfter(s, "?")
		for _, sp := range subParts {
			sp = strings.TrimSpace(sp)
			if sp == "" {
				continue
			}

			// Further split on exclamation points
			subSubParts := strings.SplitAfter(sp, "!")
			for _, ssp := range subSubParts {
				ssp = strings.TrimSpace(ssp)
				if ssp != "" {
					result = append(result, ssp)
				}
			}
		}
	}

	// If no sentence boundaries found, return whole text
	if len(result) == 0 {
		return []string{text}
	}

	return result
}
