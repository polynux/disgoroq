package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"io"
	"net/http"
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
	req, err := http.NewRequestWithContext(ctx, "GET", gifURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download GIF: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download GIF: status %d", resp.StatusCode)
	}

	gifData, err := gif.DecodeAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to decode GIF: %w", err)
	}

	if len(gifData.Image) < 2 {
		return "", nil
	}

	frames, err := gp.extractFrames(gifData)
	if err != nil {
		return "", fmt.Errorf("failed to extract frames: %w", err)
	}

	grid, err := gp.createGrid(frames)
	if err != nil {
		return "", fmt.Errorf("failed to create grid: %w", err)
	}

	base64Data, err := gp.encodeToBase64(grid)
	if err != nil {
		return "", fmt.Errorf("failed to encode grid: %w", err)
	}

	return base64Data, nil
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
	switch {
	case frameCount <= 4:
		return 2, 2
	case frameCount <= 6:
		return 2, 3
	case frameCount <= 9:
		return 3, 3
	default:
		return 3, 3
	}
}

// createGrid creates a grid image from frames
func (gp *GIFProcessor) createGrid(frames []image.Image) (image.Image, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames to create grid")
	}

	rows, cols := gp.calculateGridDimensions(len(frames))
	padding := 5

	// Find max frame dimensions
	maxWidth, maxHeight := 0, 0
	for _, frame := range frames {
		bounds := frame.Bounds()
		if bounds.Dx() > maxWidth {
			maxWidth = bounds.Dx()
		}
		if bounds.Dy() > maxHeight {
			maxHeight = bounds.Dy()
		}
	}

	// Calculate grid dimensions
	gridWidth := cols*maxWidth + (cols+1)*padding
	gridHeight := rows*maxHeight + (rows+1)*padding

	// Create destination image with white background
	dst := image.NewRGBA(image.Rect(0, 0, gridWidth, gridHeight))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)

	// Place frames in grid
	for i, frame := range frames {
		row := i / cols
		col := i % cols

		// Calculate position with padding
		x := padding + col*(maxWidth+padding)
		y := padding + row*(maxHeight+padding)

		// Define destination rectangle
		dstRect := image.Rect(x, y, x+maxWidth, y+maxHeight)

		// Draw frame
		draw.Draw(dst, dstRect, frame, frame.Bounds().Min, draw.Over)
	}

	return dst, nil
}

// encodeToBase64 encodes an image to base64 JPEG string
func (gp *GIFProcessor) encodeToBase64(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: gp.jpegQuality}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
