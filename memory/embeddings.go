package memory

import (
	"encoding/binary"
	"fmt"
	"math"
)

// F32_BLOB format constants
const (
	f32BlobTypeByte   = 0x00 // Type byte for F32_BLOB
	f32BlobHeaderSize = 3    // 1 byte type + 2 bytes dimension
)

// float32SliceToF32Blob converts a []float32 to F32_BLOB format for libsql
// Format: [type_byte: 0x00][dimension: uint16 LE][float32_values: N*4 bytes LE]
func float32SliceToF32Blob(embedding []float32) ([]byte, error) {
	if len(embedding) == 0 {
		return nil, nil
	}

	// Validate embedding dimensions
	if len(embedding) != 768 {
		return nil, fmt.Errorf("expected 768 dimensions, got %d", len(embedding))
	}

	// Calculate total size: header + float32 data
	totalSize := f32BlobHeaderSize + len(embedding)*4
	blob := make([]byte, totalSize)

	// Write header
	blob[0] = f32BlobTypeByte
	binary.LittleEndian.PutUint16(blob[1:3], uint16(len(embedding)))

	// Write float32 values in little-endian format
	for i, val := range embedding {
		binary.LittleEndian.PutUint32(blob[f32BlobHeaderSize+i*4:], math.Float32bits(val))
	}

	return blob, nil
}

// f32BlobToFloat32Slice converts F32_BLOB format to []float32
func f32BlobToFloat32Slice(blob interface{}) ([]float32, error) {
	if blob == nil {
		return nil, nil
	}

	// Handle different blob types that SQLC might return
	var blobBytes []byte
	switch v := blob.(type) {
	case []byte:
		blobBytes = v
	case *[]byte:
		if v == nil || *v == nil {
			return nil, nil
		}
		blobBytes = *v
	default:
		return nil, fmt.Errorf("unsupported blob type: %T", blob)
	}

	if len(blobBytes) == 0 {
		return nil, nil
	}

	// Validate minimum size
	if len(blobBytes) < f32BlobHeaderSize {
		return nil, fmt.Errorf("blob too small: %d bytes", len(blobBytes))
	}

	// Validate type byte
	if blobBytes[0] != f32BlobTypeByte {
		return nil, fmt.Errorf("invalid blob type: expected 0x%02x, got 0x%02x", f32BlobTypeByte, blobBytes[0])
	}

	// Read dimension
	dimension := binary.LittleEndian.Uint16(blobBytes[1:3])
	expectedSize := f32BlobHeaderSize + int(dimension)*4

	// Validate total size
	if len(blobBytes) != expectedSize {
		return nil, fmt.Errorf("blob size mismatch: expected %d bytes, got %d bytes", expectedSize, len(blobBytes))
	}

	// Validate expected dimension
	if dimension != 768 {
		return nil, fmt.Errorf("expected 768 dimensions, got %d", dimension)
	}

	// Convert to float32 slice
	embedding := make([]float32, dimension)
	for i := 0; i < int(dimension); i++ {
		bits := binary.LittleEndian.Uint32(blobBytes[f32BlobHeaderSize+i*4:])
		embedding[i] = math.Float32frombits(bits)
	}

	return embedding, nil
}

// CosineSimilarity calculates cosine similarity between two embeddings
// Returns value between -1 (opposite) and 1 (identical)
func CosineSimilarity(a, b []float32) (float32, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("embedding dimensions mismatch: %d vs %d", len(a), len(b))
	}

	if len(a) == 0 {
		return 0, fmt.Errorf("empty embeddings")
	}

	var dotProduct, normA, normB float32
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	// Handle zero norms
	if normA == 0 || normB == 0 {
		return 0, fmt.Errorf("zero norm in embedding")
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB)))), nil
}

// CosineDistance calculates cosine distance between two embeddings
// Returns value between 0 (identical) and 2 (opposite)
func CosineDistance(a, b []float32) (float32, error) {
	similarity, err := CosineSimilarity(a, b)
	if err != nil {
		return 0, err
	}
	return 1 - similarity, nil
}

// NormalizeEmbedding normalizes an embedding to unit length
func NormalizeEmbedding(embedding []float32) []float32 {
	if len(embedding) == 0 {
		return embedding
	}

	// Calculate norm
	var norm float32
	for _, val := range embedding {
		norm += val * val
	}
	norm = float32(math.Sqrt(float64(norm)))

	// Handle zero norm
	if norm == 0 {
		return embedding
	}

	// Normalize
	normalized := make([]float32, len(embedding))
	for i, val := range embedding {
		normalized[i] = val / norm
	}
	return normalized
}

// ValidateEmbedding validates embedding format and dimensions
func ValidateEmbedding(embedding []float32) error {
	if len(embedding) == 0 {
		return fmt.Errorf("empty embedding")
	}

	if len(embedding) != 768 {
		return fmt.Errorf("expected 768 dimensions, got %d", len(embedding))
	}

	// Check for NaN or Inf values
	for i, val := range embedding {
		if math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
			return fmt.Errorf("invalid value at index %d: %f", i, val)
		}
	}

	return nil
}

// EmbeddingStats contains statistics about an embedding
type EmbeddingStats struct {
	Min       float32
	Max       float32
	Mean      float32
	StdDev    float32
	Dimension int
}

// GetEmbeddingStats calculates statistics for an embedding
func GetEmbeddingStats(embedding []float32) (*EmbeddingStats, error) {
	if len(embedding) == 0 {
		return nil, fmt.Errorf("empty embedding")
	}

	stats := &EmbeddingStats{
		Dimension: len(embedding),
	}

	// Calculate min, max, mean
	stats.Min = embedding[0]
	stats.Max = embedding[0]
	var sum float32
	for _, val := range embedding {
		sum += val
		if val < stats.Min {
			stats.Min = val
		}
		if val > stats.Max {
			stats.Max = val
		}
	}
	stats.Mean = sum / float32(len(embedding))

	// Calculate standard deviation
	var variance float32
	for _, val := range embedding {
		diff := val - stats.Mean
		variance += diff * diff
	}
	stats.StdDev = float32(math.Sqrt(float64(variance / float32(len(embedding)))))

	return stats, nil
}

// CompareEmbeddings compares two embeddings and returns detailed comparison
func CompareEmbeddings(a, b []float32) (string, error) {
	if len(a) != len(b) {
		return "", fmt.Errorf("dimension mismatch: %d vs %d", len(a), len(b))
	}

	similarity, err := CosineSimilarity(a, b)
	if err != nil {
		return "", err
	}

	distance, err := CosineDistance(a, b)
	if err != nil {
		return "", err
	}

	statsA, err := GetEmbeddingStats(a)
	if err != nil {
		return "", err
	}

	statsB, err := GetEmbeddingStats(b)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`Embedding Comparison:
  Similarity: %.4f (%.2f%%)
  Distance: %.4f
  
  Embedding A:
    Min: %.4f, Max: %.4f, Mean: %.4f, StdDev: %.4f
  
  Embedding B:
    Min: %.4f, Max: %.4f, Mean: %.4f, StdDev: %.4f`,
		similarity, similarity*100, distance,
		statsA.Min, statsA.Max, statsA.Mean, statsA.StdDev,
		statsB.Min, statsB.Max, statsB.Mean, statsB.StdDev), nil
}
