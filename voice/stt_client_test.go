package voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startTestWhisperServer(t *testing.T, handler func(WhisperRequest) WhisperResponse) string {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "whisper.sock")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				for {
					lengthBuf := make([]byte, 4)
					if _, err := conn.Read(lengthBuf); err != nil {
						return
					}
					length := binary.BigEndian.Uint32(lengthBuf)
					payload := make([]byte, length)
					if _, err := conn.Read(payload); err != nil {
						return
					}
					var req WhisperRequest
					if err := json.Unmarshal(payload, &req); err != nil {
						return
					}
					respBytes, err := json.Marshal(handler(req))
					if err != nil {
						return
					}
					respLen := make([]byte, 4)
					binary.BigEndian.PutUint32(respLen, uint32(len(respBytes)))
					_, _ = conn.Write(respLen)
					_, _ = conn.Write(respBytes)
				}
			}(conn)
		}
	}()

	return socketPath
}

func TestWhisperClientTranscribe(t *testing.T) {
	var captured WhisperRequest
	socketPath := startTestWhisperServer(t, func(req WhisperRequest) WhisperResponse {
		captured = req
		return WhisperResponse{Text: "bonjour", Language: req.Language, Confidence: 0.9}
	})

	client := NewWhisperClient(WhisperConfig{SocketPath: socketPath, Model: "tiny", Language: "fr"})
	text, err := client.Transcribe(context.Background(), []byte{1, 2, 3})
	require.NoError(t, err)
	assert.Equal(t, "bonjour", text)
	assert.Equal(t, "transcribe", captured.Command)
	assert.Equal(t, "tiny", captured.Model)
	assert.Equal(t, "fr", captured.Language)
	assert.Equal(t, []byte{1, 2, 3}, captured.Audio)
	assert.Equal(t, 16000, captured.SampleRate)
	assert.Equal(t, 1, captured.Channels)
}

func TestWhisperClientStreamingLifecycle(t *testing.T) {
	var commands []string
	var stopAudio []byte
	socketPath := startTestWhisperServer(t, func(req WhisperRequest) WhisperResponse {
		commands = append(commands, req.Command)
		if req.Command == "stop" {
			stopAudio = append([]byte(nil), req.Audio...)
			return WhisperResponse{Text: "fin"}
		}
		return WhisperResponse{Text: "ok"}
	})

	client := NewWhisperClient(WhisperConfig{SocketPath: socketPath, Model: "tiny", Language: "fr"})
	require.NoError(t, client.StartStreaming(context.Background()))
	require.NoError(t, client.SendAudio([]byte{4, 5, 6}))
	text, err := client.StopStreaming()
	require.NoError(t, err)
	assert.Equal(t, "fin", text)
	assert.Equal(t, []string{"start", "stop"}, commands)
	assert.Equal(t, []byte{4, 5, 6}, stopAudio)
	assert.False(t, client.streaming)
}

func TestWhisperClientTranscribeUnavailableSocket(t *testing.T) {
	client := NewWhisperClient(WhisperConfig{SocketPath: filepath.Join(t.TempDir(), "missing.sock")})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.Transcribe(ctx, []byte{1})
	assert.ErrorIs(t, err, ErrSTTUnavailable)
}
