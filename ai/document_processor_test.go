package ai

import (
	"testing"
)

func TestNewDocumentProcessor(t *testing.T) {
	mockProvider := &mockProviderForTest{}
	dp := NewDocumentProcessor(mockProvider)

	if dp == nil {
		t.Fatal("NewDocumentProcessor() returned nil")
	}
	if dp.maxSizeMB != 50 {
		t.Errorf("expected maxSizeMB=50, got %d", dp.maxSizeMB)
	}
	if dp.maxSummaryTokens != 500 {
		t.Errorf("expected maxSummaryTokens=500, got %d", dp.maxSummaryTokens)
	}
}

func TestDocumentProcessor_CanProcess(t *testing.T) {
	mockProvider := &mockProviderForTest{}
	dp := NewDocumentProcessor(mockProvider)

	// Test supported types
	if !dp.CanProcess("application/pdf") {
		t.Error("Expected PDF to be supported")
	}
	if !dp.CanProcess("text/plain") {
		t.Error("Expected TXT to be supported")
	}

	// Test unsupported type
	if dp.CanProcess("image/jpeg") {
		t.Error("Expected JPEG to not be supported")
	}
}

func TestDocumentProcessor_ProcessDocument(t *testing.T) {
	// TODO: Add tests in Tasks 2-6
}
