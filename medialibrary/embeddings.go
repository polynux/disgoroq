package medialibrary

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

const (
	// EmbeddingDim is the vector dimension for nomic-embed-text embeddings.
	EmbeddingDim = 768

	// SearchDocumentPrefix is the nomic-embed-text prefix for indexed documents.
	SearchDocumentPrefix = "search_document: "

	// SearchQueryPrefix is the nomic-embed-text prefix for search queries.
	SearchQueryPrefix = "search_query: "
)

// Embedder generates 768-dim embeddings for text (implemented by memory.OllamaEmbeddingProvider).
type Embedder interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
}

// EncodeF32Blob encodes a 768-dim float32 slice as raw little-endian bytes for
// libSQL F32_BLOB(768) columns and vector32(?) parameters.
func EncodeF32Blob(vec []float32) ([]byte, error) {
	if len(vec) == 0 {
		return nil, nil
	}
	if len(vec) != EmbeddingDim {
		return nil, fmt.Errorf("expected %d embedding dimensions, got %d", EmbeddingDim, len(vec))
	}
	blob := make([]byte, EmbeddingDim*4)
	for i, v := range vec {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("invalid embedding value at index %d: %v", i, v)
		}
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(v))
	}
	return blob, nil
}

// DecodeF32Blob decodes a raw libSQL F32_BLOB(768) value into a float32 slice.
func DecodeF32Blob(raw any) ([]float32, error) {
	blob, err := rawBlobBytes(raw)
	if err != nil || len(blob) == 0 {
		return nil, err
	}
	if len(blob) != EmbeddingDim*4 {
		return nil, fmt.Errorf("unexpected embedding blob size: expected %d bytes, got %d", EmbeddingDim*4, len(blob))
	}
	vec := make([]float32, EmbeddingDim)
	for i := 0; i < EmbeddingDim; i++ {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return vec, nil
}

func rawBlobBytes(raw any) ([]byte, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []byte:
		if len(v) == 0 {
			return nil, nil
		}
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	case *[]byte:
		if v == nil || len(*v) == 0 {
			return nil, nil
		}
		out := make([]byte, len(*v))
		copy(out, *v)
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported embedding blob type: %T", raw)
	}
}

// EmbedDocument embeds a document string using the nomic search_document prefix.
// Empty text returns (nil, nil) without calling the embedder.
func EmbedDocument(ctx context.Context, embedder Embedder, text string) ([]byte, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}
	if embedder == nil {
		return nil, fmt.Errorf("embedding provider is not configured")
	}
	vec, err := embedder.GenerateEmbedding(ctx, SearchDocumentPrefix+trimmed)
	if err != nil {
		return nil, err
	}
	return EncodeF32Blob(vec)
}

// EmbedQuery embeds a search query string using the nomic search_query prefix.
func EmbedQuery(ctx context.Context, embedder Embedder, text string) ([]byte, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("query text cannot be empty")
	}
	if embedder == nil {
		return nil, fmt.Errorf("embedding provider is not configured")
	}
	vec, err := embedder.GenerateEmbedding(ctx, SearchQueryPrefix+trimmed)
	if err != nil {
		return nil, err
	}
	return EncodeF32Blob(vec)
}
