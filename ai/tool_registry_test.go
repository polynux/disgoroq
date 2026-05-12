package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolRuntimeConfigValidate(t *testing.T) {
	cfg := ToolRuntimeConfig{
		Enabled:          true,
		MaxRounds:        2,
		MaxCallsPerRound: 1,
		MaxCallsTotal:    2,
		Timeout:          time.Second,
		Web: WebToolConfig{
			Enabled:        true,
			AllowedSchemes: []string{"http", "https"},
			UserAgent:      "test-agent",
			MaxBytes:       4096,
			MaxCharacters:  1024,
		},
		Search: SearchToolConfig{
			Enabled:       true,
			Provider:      "searxng",
			BaseURL:       "http://localhost:8081",
			UserAgent:     "test-agent",
			MaxResults:    5,
			MaxCharacters: 1024,
			SafeSearch:    1,
		},
	}

	require.NoError(t, cfg.Validate())

	cfg.MaxRounds = 0
	require.Error(t, cfg.Validate())
}

func TestToolRegistryDefinitions(t *testing.T) {
	registry := NewToolRegistry(ToolRuntimeConfig{
		Enabled:          true,
		MaxRounds:        2,
		MaxCallsPerRound: 1,
		MaxCallsTotal:    2,
		Timeout:          time.Second,
		Web: WebToolConfig{
			Enabled:        true,
			AllowedSchemes: []string{"http", "https"},
			UserAgent:      "test-agent",
			MaxBytes:       4096,
			MaxCharacters:  1024,
		},
		Search: SearchToolConfig{
			Enabled:       true,
			Provider:      "searxng",
			BaseURL:       "http://localhost:8081",
			UserAgent:     "test-agent",
			MaxResults:    5,
			MaxCharacters: 1024,
			SafeSearch:    1,
		},
	})

	require.True(t, registry.Enabled())
	definitions := registry.Definitions()
	require.Len(t, definitions, 2)
	assert.Equal(t, defaultWebToolName, definitions[0].Function.Name)
	assert.Equal(t, defaultWebSearchToolName, definitions[1].Function.Name)
}

func TestToolRegistryExecuteUnsupportedTool(t *testing.T) {
	registry := NewToolRegistry(ToolRuntimeConfig{
		Enabled:          true,
		MaxRounds:        2,
		MaxCallsPerRound: 1,
		MaxCallsTotal:    2,
		Timeout:          time.Second,
		Web: WebToolConfig{
			Enabled:        true,
			AllowedSchemes: []string{"http", "https"},
			UserAgent:      "test-agent",
			MaxBytes:       4096,
			MaxCharacters:  1024,
		},
	})

	result, err := registry.Execute(context.Background(), ToolCall{
		ID: "call-1",
		Function: ToolFunctionCall{
			Name:      "unknown_tool",
			Arguments: `{}`,
		},
	})

	require.Error(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "unsupported tool")
}

func TestWebSearchToolReturnsResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-agent", r.Header.Get("User-Agent"))
		assert.Equal(t, "json", r.URL.Query().Get("format"))
		assert.Equal(t, "golang tool calling", r.URL.Query().Get("q"))
		assert.Equal(t, "fr", r.URL.Query().Get("language"))
		assert.Equal(t, "1", r.URL.Query().Get("safesearch"))

		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"query":"golang tool calling","results":[{"url":"https://example.com/one","title":"First result","content":"First snippet about tool calls.","engine":"duckduckgo","engines":["duckduckgo"],"category":"general"},{"url":"https://example.com/two","title":"Second result","content":"Second snippet.","engines":["wikipedia","duckduckgo"],"category":"general"}]}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	tool := NewWebSearchTool(SearchToolConfig{
		Enabled:         true,
		Provider:        "searxng",
		BaseURL:         server.URL,
		UserAgent:       "test-agent",
		MaxResults:      5,
		MaxCharacters:   1024,
		DefaultLanguage: "fr",
		SafeSearch:      1,
	})

	result, err := tool.Execute(context.Background(), ToolCall{
		ID: "call-1",
		Function: ToolFunctionCall{
			Name:      defaultWebSearchToolName,
			Arguments: `{"query":"golang tool calling","max_results":2}`,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "## Web search results")
	assert.Contains(t, result.Content, "[First result](https://example.com/one)")
	assert.Contains(t, result.Content, "Snippet: First snippet about tool calls.")
	assert.Contains(t, result.Content, "Engines: duckduckgo")
	assert.Contains(t, result.Content, "[Second result](https://example.com/two)")
}

func TestWebFetchToolReturnsMarkdownForHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-agent", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := io.WriteString(w, `<!doctype html><html><head><title>Example title</title></head><body><h1>Hello</h1><p>This is a <a href="https://example.com">test page</a>.</p><ul><li>One</li><li>Two</li></ul></body></html>`)
		require.NoError(t, err)
	}))
	defer server.Close()

	tool := NewWebFetchTool(WebToolConfig{
		Enabled:              true,
		AllowedSchemes:       []string{"http", "https"},
		UserAgent:            "test-agent",
		MaxBytes:             4096,
		MaxCharacters:        1024,
		AllowPrivateNetworks: true,
	})

	result, err := tool.Execute(context.Background(), ToolCall{
		ID: "call-1",
		Function: ToolFunctionCall{
			Name:      defaultWebToolName,
			Arguments: `{"url":"` + server.URL + `"}`,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "## Web fetch result")
	assert.Contains(t, result.Content, "# Example title")
	assert.Contains(t, result.Content, "Hello")
	assert.Contains(t, result.Content, "[test page](https://example.com)")
	assert.Contains(t, result.Content, "- One")
}

func TestWebFetchToolBlocksPrivateTargetsByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be reached", http.StatusInternalServerError)
	}))
	defer server.Close()

	tool := NewWebFetchTool(WebToolConfig{
		Enabled:        true,
		AllowedSchemes: []string{"http", "https"},
		UserAgent:      "test-agent",
		MaxBytes:       4096,
		MaxCharacters:  1024,
	})

	result, err := tool.Execute(context.Background(), ToolCall{
		ID: "call-1",
		Function: ToolFunctionCall{
			Name:      defaultWebToolName,
			Arguments: `{"url":"` + server.URL + `"}`,
		},
	})

	require.Error(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.True(t, strings.Contains(result.Content, "blocked private") || strings.Contains(result.Content, "download"))
}
