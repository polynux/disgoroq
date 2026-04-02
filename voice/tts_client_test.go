package voice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTTSHTTPClientStreamUsesConfiguredModelAndVoice(t *testing.T) {
	var captured TTSGenerateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/tts/generate", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte("wav-data"))
	}))
	defer server.Close()

	client := NewTTSHTTPClient(TTSHTTPConfig{
		Endpoint:     server.URL,
		Model:        "qwen-test",
		DefaultVoice: "narrator",
		SampleRate:   24000,
		TimeoutMs:    1000,
	})

	body, err := client.Stream(context.Background(), &TTSRequest{Text: "salut"})
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, []byte("wav-data"), data)
	assert.Equal(t, "qwen-test", captured.Model)
	assert.Equal(t, "narrator", captured.VoiceID)
	assert.Equal(t, "salut", captured.Input)
	assert.Equal(t, "wav", captured.ResponseFormat)
	assert.True(t, client.IsModelLoaded())
}

func TestTTSHTTPClientStreamStreamingUsesRequestVoice(t *testing.T) {
	var captured TTSGenerateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte{1, 2, 3})
	}))
	defer server.Close()

	client := NewTTSHTTPClient(TTSHTTPConfig{
		Endpoint:     server.URL,
		Model:        "qwen-stream",
		DefaultVoice: "default-voice",
		SampleRate:   22050,
		TimeoutMs:    1000,
	})

	chunks, err := client.StreamStreaming(context.Background(), &TTSRequest{Text: "bonjour", VoiceID: "custom-voice"})
	require.NoError(t, err)
	chunk := <-chunks
	require.NoError(t, chunk.Err)
	assert.Equal(t, []byte{1, 2, 3}, chunk.Data)
	assert.Equal(t, 22050, chunk.SampleRate)
	assert.Equal(t, "qwen-stream", captured.Model)
	assert.Equal(t, "custom-voice", captured.VoiceID)
	assert.True(t, captured.Stream)
	assert.Equal(t, "pcm", captured.ResponseFormat)
}

func TestTTSHTTPClientLoadAndUnloadModel(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewTTSHTTPClient(TTSHTTPConfig{Endpoint: server.URL, TimeoutMs: 1000})
	require.NoError(t, client.LoadModel(context.Background()))
	assert.True(t, client.IsModelLoaded())
	require.NoError(t, client.UnloadModel(context.Background()))
	assert.False(t, client.IsModelLoaded())
	assert.Equal(t, []string{"/load", "/unload"}, calls)
}
