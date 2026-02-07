# GIF Grid Processing - Learnings

## Technical Decisions

### Frame Extraction Strategy
- Extract 4-9 frames based on GIF length
- Formula: `max(4, min(9, totalFrames/3))`
- Evenly spaced indices calculated using floating point division
- This provides good visual representation without overwhelming the vision model

### Grid Layout
- Adaptive grid sizing based on frame count:
  - 4 frames → 2x2
  - 5-6 frames → 2x3
  - 7-9 frames → 3x3
- 5px padding between frames for visual separation
- White background for consistent appearance

### Base64 Encoding
- JPEG format chosen for smaller size vs PNG
- Quality 85 provides good balance of size and clarity
- Base64 data URIs work with Groq vision API

## Implementation Details

### Package Structure
- `ai/gif_processor.go` - Core GIF processing logic
- `ai/gif_processor_test.go` - Unit tests
- Integrated with `ai/context.go` for message processing

### Key Functions
- `IsAnimatedGIF()` - Detects animated GIFs by frame count
- `extractFrames()` - Extracts evenly spaced frames (4-9)
- `calculateGridDimensions()` - Determines grid layout
- `createGrid()` - Composes frames into grid image
- `encodeToBase64()` - JPEG encoding for vision API
- `ProcessGIF()` - Full pipeline from URL to base64 grid

## Challenges Encountered

### GIF Decoding
- Standard library `image/gif` works well for most GIFs
- Need to handle `gif.DecodeAll()` to get all frames
- Paletted images require proper color handling in tests

### Image Composition
- Standard library `image/draw` sufficient for grid creation
- No external dependencies needed
- Uniform frame sizing simplifies grid layout

### Integration with Context Builder
- Context parameter added to support cancellation/timeouts
- HTTP download with context support
- Graceful degradation on processing errors (fall back to original GIF)

## Performance Considerations

### Memory Usage
- Grid memory: width × height × 4 bytes
- Frame extraction holds all frames in memory temporarily
- Base64 encoding increases size by ~33%

### HTTP Download
- Uses `http.DefaultClient` with context
- Supports timeout through context cancellation
- Error handling for network failures

## Test Coverage

### Unit Tests
- `TestNewGIFProcessor` - Configuration initialization
- `TestGIFProcessor_IsAnimatedGIF` - Animation detection
- `TestGIFProcessor_calculateEvenIndices` - Frame index calculation
- `TestGIFProcessor_extractFrames` - Frame extraction
- `TestGIFProcessor_extractFrames_SingleFrame` - Edge case
- `TestGIFProcessor_calculateGridDimensions` - Grid sizing
- `TestGIFProcessor_createGrid` - Grid composition
- `TestGIFProcessor_createGrid_EmptyFrames` - Error handling
- `TestGIFProcessor_ProcessGIF` - Full pipeline (placeholder)

### Edge Cases Covered
- Static GIFs (1 frame)
- Minimum animated GIFs (2 frames)
- Large GIFs (100+ frames)
- Empty frame lists
- Different frame sizes
- Missing/empty frames

## Integration Points

### Context Builder
- `getImagesToProcess()` now accepts context parameter
- Checks for animated GIFs before adding to processing list
- Converts animated GIFs to base64 JPEG grids
- Static GIFs pass through unchanged

### Vision API
- Base64 data URIs in format: `data:image/jpeg;base64,<data>`
- JPEG format compatible with Groq vision API
- Grid images provide multiple frames in single API call

## Commits

1. `5c3b270` - feat(ai): add GIF processor module structure
2. `aefe083` - feat(ai): implement GIF frame extraction
3. `0c0bede` - feat(ai): implement GIF frame grid composition
4. `77e853f` - feat(ai): integrate GIF processor with context builder

## Future Improvements

- Support for WebP animated images
- Concurrent frame processing for very large GIFs
- Configurable grid padding
- Frame deduplication (skip visually similar frames)
- Progress callback for large GIFs
- Caching of processed GIFs (if needed)
