package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// WhisperClient implements STTClient for whisper.cpp via Unix socket.
type WhisperClient struct {
	socketPath     string
	model          string
	language       string
	conn           *net.UnixConn
	mu             sync.Mutex
	streaming      bool
	streamBuffer   []byte
	connected      bool
	reconnectDelay time.Duration
}

// WhisperConfig contains configuration for the whisper client.
type WhisperConfig struct {
	SocketPath string
	Model      string
	Language   string
}

// WhisperRequest represents a request to whisper.cpp.
type WhisperRequest struct {
	Command    string `json:"command"`               // "transcribe", "start", "stop", "reset"
	Audio      []byte `json:"audio"`                 // Audio data (PCM 16-bit)
	Language   string `json:"language,omitempty"`    // Language code (e.g., "fr", "en")
	Model      string `json:"model,omitempty"`       // Model name
	SampleRate int    `json:"sample_rate,omitempty"` // Audio sample rate (default: 16000)
	Channels   int    `json:"channels,omitempty"`    // Number of channels (default: 1)
}

// WhisperResponse represents a response from whisper.cpp.
type WhisperResponse struct {
	Text       string  `json:"text"`
	Language   string  `json:"language"`
	Confidence float64 `json:"confidence"`
	Duration   float64 `json:"duration"`
	Error      string  `json:"error,omitempty"`
}

// NewWhisperClient creates a new whisper.cpp client.
func NewWhisperClient(config WhisperConfig) *WhisperClient {
	return &WhisperClient{
		socketPath:     config.SocketPath,
		model:          config.Model,
		language:       config.Language,
		streamBuffer:   make([]byte, 0),
		reconnectDelay: 1 * time.Second,
	}
}

// Connect establishes a connection to the whisper.cpp server.
// NOTE: This must be called with the mutex already held (from Transcribe).
func (c *WhisperClient) Connect(ctx context.Context) error {
	if c.connected {
		return nil
	}

	logger.Info("Connecting to whisper socket", zap.String("socket", c.socketPath))

	addr := &net.UnixAddr{Name: c.socketPath, Net: "unix"}
	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		logger.Error("Failed to connect to whisper socket",
			zap.String("socket", c.socketPath),
			zap.Error(err))
		return fmt.Errorf("failed to connect to whisper socket: %w", err)
	}

	// Set read/write deadlines
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		// Default 60 second timeout
		conn.SetDeadline(time.Now().Add(60 * time.Second))
	}

	c.conn = conn
	c.connected = true

	logger.Info("Connected to whisper.cpp server",
		zap.String("socket", c.socketPath))

	return nil
}

// Transcribe sends audio data for transcription and returns the text.
func (c *WhisperClient) Transcribe(ctx context.Context, audio []byte) (string, error) {
	logger.Debug("Acquiring STT client lock...")
	c.mu.Lock()
	defer c.mu.Unlock()

	logger.Debug("STT client lock acquired")

	if !c.connected {
		logger.Info("STT not connected, attempting to connect",
			zap.String("socket", c.socketPath))
		if err := c.Connect(ctx); err != nil {
			logger.Error("Failed to connect to whisper socket",
				zap.String("socket", c.socketPath),
				zap.Error(err))
			return "", ErrSTTUnavailable
		}
	}

	// Set deadline from context or default 60s
	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
	} else {
		c.conn.SetDeadline(time.Now().Add(60 * time.Second))
	}

	logger.Info("Sending transcription request",
		zap.Int("audio_bytes", len(audio)),
		zap.String("language", c.language),
		zap.String("model", c.model))

	// Build request
	req := WhisperRequest{
		Command:    "transcribe",
		Audio:      audio,
		Language:   c.language,
		Model:      c.model,
		SampleRate: 16000, // Whisper expects 16kHz
		Channels:   1,     // Mono audio
	}

	// Send request
	logger.Debug("Sending request to whisper server...")
	if err := c.sendRequest(req); err != nil {
		logger.Error("Failed to send STT request", zap.Error(err))
		c.connected = false
		return "", err
	}
	logger.Debug("Request sent, waiting for response...")

	// Read response
	resp, err := c.readResponse()
	if err != nil {
		logger.Error("Failed to read STT response", zap.Error(err))
		c.connected = false
		return "", err
	}
	logger.Debug("Response received from whisper server")

	if resp.Error != "" {
		logger.Error("Whisper returned error",
			zap.String("error", resp.Error))
		return "", fmt.Errorf("whisper error: %s", resp.Error)
	}

	logger.Info("Transcription complete",
		zap.String("text", resp.Text),
		zap.Float64("confidence", resp.Confidence),
		zap.String("language", resp.Language))

	return resp.Text, nil
}

// StartStreaming starts a streaming transcription session.
func (c *WhisperClient) StartStreaming(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		if err := c.Connect(ctx); err != nil {
			return ErrSTTUnavailable
		}
	}

	if c.streaming {
		return nil
	}

	// Send start streaming command
	req := WhisperRequest{
		Command:  "start",
		Language: c.language,
		Model:    c.model,
	}

	if err := c.sendRequest(req); err != nil {
		return err
	}

	// Read acknowledgment
	resp, err := c.readResponse()
	if err != nil {
		return err
	}

	if resp.Error != "" {
		return fmt.Errorf("failed to start streaming: %s", resp.Error)
	}

	c.streaming = true
	c.streamBuffer = c.streamBuffer[:0]

	logger.Debug("Started streaming transcription")

	return nil
}

// StopStreaming stops the streaming session and returns the final transcription.
func (c *WhisperClient) StopStreaming() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.streaming {
		return "", nil
	}

	// Send stop command with buffered audio
	req := WhisperRequest{
		Command:  "stop",
		Audio:    c.streamBuffer,
		Language: c.language,
	}

	if err := c.sendRequest(req); err != nil {
		c.streaming = false
		return "", err
	}

	// Read final response
	resp, err := c.readResponse()
	if err != nil {
		c.streaming = false
		return "", err
	}

	c.streaming = false
	c.streamBuffer = c.streamBuffer[:0]

	if resp.Error != "" {
		return "", fmt.Errorf("streaming error: %s", resp.Error)
	}

	logger.Debug("Stopped streaming transcription",
		zap.String("text", resp.Text))

	return resp.Text, nil
}

// SendAudio sends audio data to an active streaming session.
func (c *WhisperClient) SendAudio(audio []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.streaming {
		return fmt.Errorf("not in streaming mode")
	}

	// Buffer the audio
	c.streamBuffer = append(c.streamBuffer, audio...)

	return nil
}

// IsConnected returns whether the client is connected to the STT service.
func (c *WhisperClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Close closes the connection to the STT service.
func (c *WhisperClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.streaming {
		// Try to stop streaming gracefully
		req := WhisperRequest{Command: "reset"}
		_ = c.sendRequest(req)
		c.streaming = false
	}

	c.connected = false

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}

	return nil
}

// sendRequest sends a request to whisper.cpp.
func (c *WhisperClient) sendRequest(req WhisperRequest) error {
	if c.conn == nil {
		return ErrSTTUnavailable
	}

	// JSON encode the request
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Write length prefix (4 bytes, big-endian)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))

	// Write length + data
	if _, err := c.conn.Write(length); err != nil {
		c.connected = false
		return fmt.Errorf("failed to write length: %w", err)
	}

	if _, err := c.conn.Write(data); err != nil {
		c.connected = false
		return fmt.Errorf("failed to write data: %w", err)
	}

	return nil
}

// readResponse reads a response from whisper.cpp.
func (c *WhisperClient) readResponse() (*WhisperResponse, error) {
	if c.conn == nil {
		return nil, ErrSTTUnavailable
	}

	// Read length prefix (4 bytes)
	lengthBuf := make([]byte, 4)
	if _, err := c.conn.Read(lengthBuf); err != nil {
		c.connected = false
		return nil, fmt.Errorf("failed to read length: %w", err)
	}

	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		return nil, fmt.Errorf("empty response")
	}

	// Read response data
	data := make([]byte, length)
	if _, err := c.conn.Read(data); err != nil {
		c.connected = false
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Decode response
	var resp WhisperResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &resp, nil
}

// MockSTTClient is a mock STT client for testing.
type MockSTTClient struct {
	Response string
	Error    error
}

// Transcribe implements STTClient.
func (m *MockSTTClient) Transcribe(ctx context.Context, audio []byte) (string, error) {
	if m.Error != nil {
		return "", m.Error
	}
	return m.Response, nil
}

// StartStreaming implements STTClient.
func (m *MockSTTClient) StartStreaming(ctx context.Context) error {
	return m.Error
}

// StopStreaming implements STTClient.
func (m *MockSTTClient) StopStreaming() (string, error) {
	if m.Error != nil {
		return "", m.Error
	}
	return m.Response, nil
}

// SendAudio implements STTClient.
func (m *MockSTTClient) SendAudio(audio []byte) error {
	return m.Error
}

// IsConnected implements STTClient.
func (m *MockSTTClient) IsConnected() bool {
	return true
}

// Close implements STTClient.
func (m *MockSTTClient) Close() error {
	return nil
}

// HealthCheck verifies the whisper server is responsive.
func (c *WhisperClient) HealthCheck(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Try to connect if not connected
	if !c.connected {
		if err := c.Connect(ctx); err != nil {
			return err
		}
	}

	// Send a ping/health check request
	req := WhisperRequest{
		Command: "health",
	}

	if err := c.sendRequest(req); err != nil {
		return err
	}

	// Read response with timeout
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.conn.SetReadDeadline(time.Time{})

	resp, err := c.readResponse()
	if err != nil {
		return err
	}

	if resp.Error != "" {
		return fmt.Errorf("health check failed: %s", resp.Error)
	}

	return nil
}
