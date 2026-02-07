package ai

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
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
	gp := NewGIFProcessor()

	// Test with static GIF (1 frame)
	staticGIF := createTestGIF(1)
	staticBuf := encodeTestGIF(staticGIF)
	isAnimated, err := gp.IsAnimatedGIF(staticBuf)
	if err != nil {
		t.Fatalf("IsAnimatedGIF failed for static GIF: %v", err)
	}
	if isAnimated {
		t.Error("Expected static GIF (1 frame) to return false")
	}

	// Test with animated GIF (5 frames)
	animatedGIF := createTestGIF(5)
	animatedBuf := encodeTestGIF(animatedGIF)
	isAnimated, err = gp.IsAnimatedGIF(animatedBuf)
	if err != nil {
		t.Fatalf("IsAnimatedGIF failed for animated GIF: %v", err)
	}
	if !isAnimated {
		t.Error("Expected animated GIF (5 frames) to return true")
	}
}

func TestGIFProcessor_calculateEvenIndices(t *testing.T) {
	gp := NewGIFProcessor()

	tests := []struct {
		name            string
		totalFrames     int
		framesToExtract int
		expectedFirst   int
		expectedLast    int
	}{
		{"10 frames, extract 4", 10, 4, 0, 9},
		{"20 frames, extract 6", 20, 6, 0, 19},
		{"100 frames, extract 9", 100, 9, 0, 99},
		{"5 frames, extract 4", 5, 4, 0, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indices := gp.calculateEvenIndices(tt.totalFrames, tt.framesToExtract)
			if len(indices) != tt.framesToExtract {
				t.Errorf("Expected %d indices, got %d", tt.framesToExtract, len(indices))
			}
			if indices[0] != tt.expectedFirst {
				t.Errorf("Expected first index %d, got %d", tt.expectedFirst, indices[0])
			}
			if indices[len(indices)-1] != tt.expectedLast {
				t.Errorf("Expected last index %d, got %d", tt.expectedLast, indices[len(indices)-1])
			}
		})
	}
}

func TestGIFProcessor_extractFrames(t *testing.T) {
	gp := NewGIFProcessor()

	tests := []struct {
		name           string
		frameCount     int
		expectedFrames int
	}{
		{"5 frames → 4 extracted", 5, 4},
		{"20 frames → 6 extracted", 20, 6},
		{"50 frames → 9 extracted (capped)", 50, 9},
		{"100 frames → 9 extracted (capped)", 100, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testGIF := createTestGIF(tt.frameCount)
			frames, err := gp.extractFrames(testGIF)
			if err != nil {
				t.Fatalf("extractFrames failed: %v", err)
			}
			if len(frames) != tt.expectedFrames {
				t.Errorf("Expected %d frames, got %d", tt.expectedFrames, len(frames))
			}
		})
	}
}

func TestGIFProcessor_extractFrames_SingleFrame(t *testing.T) {
	gp := NewGIFProcessor()
	singleFrameGIF := createTestGIF(1)
	_, err := gp.extractFrames(singleFrameGIF)
	if err == nil {
		t.Error("Expected error for single frame GIF, got nil")
	}
}

// Helper function to create test GIFs
func createTestGIF(frameCount int) *gif.GIF {
	// Create a simple palette
	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
	}

	g := &gif.GIF{
		Image: make([]*image.Paletted, frameCount),
		Delay: make([]int, frameCount),
		Config: image.Config{
			ColorModel: palette,
			Width:      100,
			Height:     100,
		},
	}

	for i := 0; i < frameCount; i++ {
		img := image.NewPaletted(image.Rect(0, 0, 100, 100), palette)
		// Fill with different colors for each frame
		for y := 0; y < 100; y++ {
			for x := 0; x < 100; x++ {
				if (x+y+i)%2 == 0 {
					img.SetColorIndex(x, y, 0)
				} else {
					img.SetColorIndex(x, y, 1)
				}
			}
		}
		g.Image[i] = img
		g.Delay[i] = 10
	}

	return g
}

// Helper function to encode GIF to bytes
func encodeTestGIF(g *gif.GIF) *bytes.Buffer {
	var buf bytes.Buffer
	if len(g.Image) == 0 {
		return &buf
	}
	gif.EncodeAll(&buf, g)
	return &buf
}

func TestGIFProcessor_calculateGridDimensions(t *testing.T) {
	gp := NewGIFProcessor()

	tests := []struct {
		frameCount   int
		expectedRows int
		expectedCols int
	}{
		{4, 2, 2},
		{5, 2, 3},
		{6, 2, 3},
		{7, 3, 3},
		{8, 3, 3},
		{9, 3, 3},
	}

	for _, tt := range tests {
		rows, cols := gp.calculateGridDimensions(tt.frameCount)
		if rows != tt.expectedRows {
			t.Errorf("frameCount=%d: expected rows=%d, got %d", tt.frameCount, tt.expectedRows, rows)
		}
		if cols != tt.expectedCols {
			t.Errorf("frameCount=%d: expected cols=%d, got %d", tt.frameCount, tt.expectedCols, cols)
		}
	}
}

func TestGIFProcessor_createGrid(t *testing.T) {
	gp := NewGIFProcessor()

	// Create 4 test frames
	frames := make([]image.Image, 4)
	for i := 0; i < 4; i++ {
		frames[i] = image.NewRGBA(image.Rect(0, 0, 100, 100))
	}

	grid, err := gp.createGrid(frames)
	if err != nil {
		t.Fatalf("createGrid failed: %v", err)
	}

	// Check grid is not nil
	if grid == nil {
		t.Fatal("createGrid returned nil image")
	}

	// Check dimensions (2x2 grid with 5px padding)
	// Expected: (2*100 + 3*5) = 215px width/height
	bounds := grid.Bounds()
	expectedSize := 2*100 + 3*5 // 215
	if bounds.Dx() != expectedSize {
		t.Errorf("expected width %d, got %d", expectedSize, bounds.Dx())
	}
	if bounds.Dy() != expectedSize {
		t.Errorf("expected height %d, got %d", expectedSize, bounds.Dy())
	}
}

func TestGIFProcessor_createGrid_EmptyFrames(t *testing.T) {
	gp := NewGIFProcessor()

	_, err := gp.createGrid([]image.Image{})
	if err == nil {
		t.Error("Expected error for empty frames, got nil")
	}
}

func TestGIFProcessor_ProcessGIF(t *testing.T) {
	// TODO: Add tests in Task 4 after full integration
}
