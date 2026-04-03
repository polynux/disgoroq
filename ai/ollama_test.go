package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOllamaProvider(t *testing.T) {
	provider, err := NewOllamaProvider("http://localhost:11434")
	require.NoError(t, err)
	assert.NotNil(t, provider)
	assert.NotNil(t, provider.client)
}

func TestNewOllamaProvider_InvalidURL(t *testing.T) {
	provider, err := NewOllamaProvider("://bad-url")
	require.Error(t, err)
	assert.Nil(t, provider)
}
