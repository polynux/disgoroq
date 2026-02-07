package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"io"
)

// GIFProcessor handles animated GIF processing for vision models
type GIFProcessor struct {
	maxFrames   int
	minFrames   int
	jpegQuality int
}

// NewGIFProcessor creates a new GIFProcessor with default configuration
func NewGIFProcessor() *GIFProcessor {
	return &GIFProcessor{
		maxFrames:   9,
		minFrames:   4,
		jpegQuality: 85,
	}
}

// IsAnimatedGIF checks if the provided reader contains an animated GIF
func (gp *GIFProcessor) IsAnimatedGIF(r io.Reader) (bool, error) {
	g, err := gif.DecodeAll(r)
	if err != nil {
		return false, err
	}
	return len(g.Image) >= 2, nil
}

// ProcessGIF processes an animated GIF and returns a base64 encoded JPEG grid
func (gp *GIFProcessor) ProcessGIF(ctx context.Context, gifURL string) (string, error) {
	// TODO: Implement in Tasks 3-4
	return "", nil
}

// extractFrames extracts evenly spaced frames from an animated GIF
func (gp *GIFProcessor) extractFrames(gifData *gif.GIF) ([]image.Image, error) {
	totalFrames := len(gifData.Image)
	if totalFrames < 2 {
		return nil, fmt.Errorf("GIF has less than 2 frames")
	}

	// Calculate frames to extract (4-9 range)
	framesToExtract := gp.minFrames
	if totalFrames/3 > gp.minFrames {
		framesToExtract = totalFrames / 3
	}
	if framesToExtract > gp.maxFrames {
		framesToExtract = gp.maxFrames
	}

	indices := gp.calculateEvenIndices(totalFrames, framesToExtract)

	frames := make([]image.Image, 0, len(indices))
	for _, idx := range indices {
		if idx < len(gifData.Image) {
			frames = append(frames, gifData.Image[idx])
		}
	}

	return frames, nil
}

// calculateEvenIndices calculates evenly spaced frame indices
func (gp *GIFProcessor) calculateEvenIndices(totalFrames, frameCount int) []int {
	if frameCount <= 1 {
		return []int{0}
	}

	indices := make([]int, frameCount)
	step := float64(totalFrames-1) / float64(frameCount-1)

	for i := 0; i < frameCount; i++ {
		indices[i] = int(float64(i) * step)
	}

	// Ensure last index is the last frame
	indices[frameCount-1] = totalFrames - 1

	return indices
}

// calculateGridDimensions determines grid size based on frame count
func (gp *GIFProcessor) calculateGridDimensions(frameCount int) (rows, cols int) {
	// TODO: Implement in Task 3
	return 0, 0
}

// createGrid creates a grid image from frames
func (gp *GIFProcessor) createGrid(frames []image.Image) (image.Image, error) {
	// TODO: Implement in Task 3
	return nil, nil
}

// encodeToBase64 encodes an image to base64 JPEG string
func (gp *GIFProcessor) encodeToBase64(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: gp.jpegQuality}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
