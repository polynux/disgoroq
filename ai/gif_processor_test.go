package ai

import (
	"testing"
)

func TestNewGIFProcessor(t *testing.T) {
	gp := NewGIFProcessor()
	if gp == nil {
		t.Fatal("NewGIFProcessor() returned nil")
	}
	if gp.maxFrames != 9 {
		t.Errorf("expected maxFrames=9, got %d", gp.maxFrames)
	}
	if gp.minFrames != 4 {
		t.Errorf("expected minFrames=4, got %d", gp.minFrames)
	}
	if gp.jpegQuality != 85 {
		t.Errorf("expected jpegQuality=85, got %d", gp.jpegQuality)
	}
}

func TestGIFProcessor_IsAnimatedGIF(t *testing.T) {
	// TODO: Add tests in Task 2
}

func TestGIFProcessor_ProcessGIF(t *testing.T) {
	// TODO: Add tests in Tasks 2-3
}
