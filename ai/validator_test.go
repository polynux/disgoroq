package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponseValidator(t *testing.T) {
	tests := []struct {
		name     string
		options  []ResponseValidatorOption
		expected *ResponseValidator
	}{
		{
			name:    "default configuration",
			options: []ResponseValidatorOption{},
			expected: &ResponseValidator{
				minLength:    1,
				trimSpaces:   true,
				checkUnicode: true,
			},
		},
		{
			name: "custom minimum length",
			options: []ResponseValidatorOption{
				WithMinLength(10),
			},
			expected: &ResponseValidator{
				minLength:    10,
				trimSpaces:   true,
				checkUnicode: true,
			},
		},
		{
			name: "disable trimming",
			options: []ResponseValidatorOption{
				WithTrimSpaces(false),
			},
			expected: &ResponseValidator{
				minLength:    1,
				trimSpaces:   false,
				checkUnicode: true,
			},
		},
		{
			name: "disable unicode check",
			options: []ResponseValidatorOption{
				WithUnicodeCheck(false),
			},
			expected: &ResponseValidator{
				minLength:    1,
				trimSpaces:   true,
				checkUnicode: false,
			},
		},
		{
			name: "multiple options",
			options: []ResponseValidatorOption{
				WithMinLength(5),
				WithTrimSpaces(false),
				WithUnicodeCheck(false),
			},
			expected: &ResponseValidator{
				minLength:    5,
				trimSpaces:   false,
				checkUnicode: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewResponseValidator(tt.options...)
			assert.Equal(t, tt.expected.minLength, validator.minLength)
			assert.Equal(t, tt.expected.trimSpaces, validator.trimSpaces)
			assert.Equal(t, tt.expected.checkUnicode, validator.checkUnicode)
		})
	}
}

func TestValidateChatResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *ChatResponse
		options  []ResponseValidatorOption
		valid    bool
		reason   string
	}{
		{
			name:     "nil response",
			response: nil,
			options:  []ResponseValidatorOption{},
			valid:    false,
			reason:   "response is nil",
		},
		{
			name: "empty content",
			response: &ChatResponse{
				Content: "",
			},
			options: []ResponseValidatorOption{},
			valid:   false,
			reason:  "content is empty string",
		},
		{
			name: "only whitespace",
			response: &ChatResponse{
				Content: "   \t\n  ",
			},
			options: []ResponseValidatorOption{},
			valid:   false,
			reason:  "content is only whitespace",
		},
		{
			name: "valid content",
			response: &ChatResponse{
				Content: "Hello, world!",
			},
			options: []ResponseValidatorOption{},
			valid:   true,
			reason:  "content is valid",
		},
		{
			name: "content too short",
			response: &ChatResponse{
				Content: "Hi",
			},
			options: []ResponseValidatorOption{
				WithMinLength(10),
			},
			valid:  false,
			reason: "content too short",
		},
		{
			name: "minimum length met",
			response: &ChatResponse{
				Content: "Hello, world!",
			},
			options: []ResponseValidatorOption{
				WithMinLength(5),
			},
			valid:  true,
			reason: "content is valid",
		},
		{
			name: "whitespace not trimmed when disabled",
			response: &ChatResponse{
				Content: "  hello  ",
			},
			options: []ResponseValidatorOption{
				WithTrimSpaces(false),
				WithMinLength(10),
			},
			valid:  false,
			reason: "content too short",
		},
		{
			name: "whitespace trimmed when enabled",
			response: &ChatResponse{
				Content: "  hello  ",
			},
			options: []ResponseValidatorOption{
				WithTrimSpaces(true),
				WithMinLength(5),
			},
			valid:  true,
			reason: "content is valid",
		},
		{
			name: "unicode characters",
			response: &ChatResponse{
				Content: "Hello 世界 🌍",
			},
			options: []ResponseValidatorOption{},
			valid:   true,
			reason:  "content is valid",
		},
		{
			name: "emoji only",
			response: &ChatResponse{
				Content: "🚀🎉🌟",
			},
			options: []ResponseValidatorOption{},
			valid:   true,
			reason:  "content is valid",
		},
		{
			name: "mixed content with whitespace",
			response: &ChatResponse{
				Content: "  Hello, world!  How are you?  ",
			},
			options: []ResponseValidatorOption{},
			valid:   true,
			reason:  "content is valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewResponseValidator(tt.options...)
			result := validator.ValidateChatResponse(tt.response)

			assert.Equal(t, tt.valid, result.IsValid, "validation result should match expected")
			assert.Equal(t, tt.reason, result.Reason, "validation reason should match expected")

			if result.IsValid {
				assert.True(t, result.Details.HasContent, "valid response should have content")
				assert.True(t, result.Details.MinLengthMet, "valid response should meet minimum length")
			}
		})
	}
}

func TestValidateVisionResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *VisionResponse
		options  []ResponseValidatorOption
		valid    bool
		reason   string
	}{
		{
			name:     "nil response",
			response: nil,
			options:  []ResponseValidatorOption{},
			valid:    false,
			reason:   "response is nil",
		},
		{
			name: "empty description",
			response: &VisionResponse{
				Description: "",
			},
			options: []ResponseValidatorOption{},
			valid:   false,
			reason:  "content is empty string",
		},
		{
			name: "valid description",
			response: &VisionResponse{
				Description: "A beautiful sunset over mountains",
			},
			options: []ResponseValidatorOption{},
			valid:   true,
			reason:  "content is valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewResponseValidator(tt.options...)
			result := validator.ValidateVisionResponse(tt.response)

			assert.Equal(t, tt.valid, result.IsValid)
			assert.Equal(t, tt.reason, result.Reason)
		})
	}
}

func TestValidationResultMethods(t *testing.T) {
	t.Run("GetValidationReason", func(t *testing.T) {
		validResult := &ValidationResult{
			IsValid: true,
			Reason:  "content is valid",
		}
		invalidResult := &ValidationResult{
			IsValid: false,
			Reason:  "content is empty string",
		}

		assert.Equal(t, "", validResult.GetValidationReason())
		assert.Equal(t, "content is empty string", invalidResult.GetValidationReason())
	})

	t.Run("String", func(t *testing.T) {
		validResult := &ValidationResult{
			IsValid: true,
			Reason:  "content is valid",
		}
		invalidResult := &ValidationResult{
			IsValid: false,
			Reason:  "content is empty string",
		}

		assert.Equal(t, "valid", validResult.String())
		assert.Equal(t, "invalid: content is empty string", invalidResult.String())
	})
}

func TestIsEmptyResponse(t *testing.T) {
	validator := NewResponseValidator()

	tests := []struct {
		name     string
		response *ChatResponse
		isEmpty  bool
	}{
		{
			name:     "nil response",
			response: nil,
			isEmpty:  true,
		},
		{
			name: "empty content",
			response: &ChatResponse{
				Content: "",
			},
			isEmpty: true,
		},
		{
			name: "whitespace only",
			response: &ChatResponse{
				Content: "   \t\n  ",
			},
			isEmpty: true,
		},
		{
			name: "valid content",
			response: &ChatResponse{
				Content: "Hello, world!",
			},
			isEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validator.IsEmptyResponse(tt.response)
			assert.Equal(t, tt.isEmpty, result)
		})
	}
}

func TestValidationDetails(t *testing.T) {
	validator := NewResponseValidator(WithMinLength(10))

	t.Run("empty content details", func(t *testing.T) {
		response := &ChatResponse{Content: ""}
		result := validator.ValidateChatResponse(response)

		require.False(t, result.IsValid)
		assert.Equal(t, 0, result.Details.ContentLength)
		assert.Equal(t, 0, result.Details.TrimmedLength)
		assert.False(t, result.Details.HasWhitespace)
		assert.False(t, result.Details.HasContent)
		assert.False(t, result.Details.MinLengthMet)
	})

	t.Run("whitespace content details", func(t *testing.T) {
		response := &ChatResponse{Content: "   \t\n  "}
		result := validator.ValidateChatResponse(response)

		require.False(t, result.IsValid)
		assert.Equal(t, 7, result.Details.ContentLength)
		assert.Equal(t, 0, result.Details.TrimmedLength)
		assert.True(t, result.Details.HasWhitespace)
		assert.False(t, result.Details.HasContent)
		assert.False(t, result.Details.MinLengthMet)
	})

	t.Run("valid content details", func(t *testing.T) {
		response := &ChatResponse{Content: "Hello, world!"}
		result := validator.ValidateChatResponse(response)

		require.True(t, result.IsValid)
		assert.Equal(t, 13, result.Details.ContentLength)
		assert.Equal(t, 13, result.Details.TrimmedLength)
		assert.False(t, result.Details.HasWhitespace)
		assert.True(t, result.Details.HasContent)
		assert.True(t, result.Details.MinLengthMet)
	})
}

func TestUnicodeValidation(t *testing.T) {
	validator := NewResponseValidator()

	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name:     "ascii text",
			content:  "Hello World",
			expected: true,
		},
		{
			name:     "unicode text",
			content:  "Hello 世界 🌍",
			expected: true,
		},
		{
			name:     "emoji only",
			content:  "🚀🎉🌟",
			expected: true,
		},
		{
			name:     "mixed with symbols",
			content:  "Test @#$%^&*()_+",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &ChatResponse{Content: tt.content}
			result := validator.ValidateChatResponse(response)
			assert.Equal(t, tt.expected, result.IsValid)
		})
	}
}

func TestEdgeCases(t *testing.T) {
	validator := NewResponseValidator()

	t.Run("single character", func(t *testing.T) {
		response := &ChatResponse{Content: "a"}
		result := validator.ValidateChatResponse(response)
		assert.True(t, result.IsValid)
	})

	t.Run("single space", func(t *testing.T) {
		response := &ChatResponse{Content: " "}
		result := validator.ValidateChatResponse(response)
		assert.False(t, result.IsValid)
		assert.Equal(t, "content is only whitespace", result.Reason)
	})

	t.Run("mixed whitespace types", func(t *testing.T) {
		response := &ChatResponse{Content: " \t\n\r "}
		result := validator.ValidateChatResponse(response)
		assert.False(t, result.IsValid)
		assert.Equal(t, "content is only whitespace", result.Reason)
	})
}

func BenchmarkValidateChatResponse(b *testing.B) {
	validator := NewResponseValidator()
	response := &ChatResponse{
		Content: "This is a test response that should be validated quickly and efficiently. It contains multiple words and some punctuation!",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.ValidateChatResponse(response)
	}
}

func BenchmarkValidateChatResponseEmpty(b *testing.B) {
	validator := NewResponseValidator()
	response := &ChatResponse{
		Content: "",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.ValidateChatResponse(response)
	}
}

func BenchmarkValidateChatResponseWhitespace(b *testing.B) {
	validator := NewResponseValidator()
	response := &ChatResponse{
		Content: "   \t\n  ",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.ValidateChatResponse(response)
	}
}
