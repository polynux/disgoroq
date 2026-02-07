package ai

import (
	"strings"
	"unicode"
)

// ValidationResult contains the validation result with details
type ValidationResult struct {
	IsValid bool
	Reason  string
	Details ValidationDetails
}

// ValidationDetails provides detailed validation information
type ValidationDetails struct {
	ContentLength int
	TrimmedLength int
	HasWhitespace bool
	HasContent    bool
	MinLengthMet  bool
}

// ResponseValidator validates AI responses for emptiness and minimum requirements
type ResponseValidator struct {
	minLength    int
	trimSpaces   bool
	checkUnicode bool
}

// ResponseValidatorOption configures the response validator
type ResponseValidatorOption func(*ResponseValidator)

// WithMinLength sets the minimum content length requirement
func WithMinLength(length int) ResponseValidatorOption {
	return func(v *ResponseValidator) {
		v.minLength = length
	}
}

// WithTrimSpaces enables whitespace trimming before validation
func WithTrimSpaces(trim bool) ResponseValidatorOption {
	return func(v *ResponseValidator) {
		v.trimSpaces = trim
	}
}

// WithUnicodeCheck enables Unicode character validation
func WithUnicodeCheck(check bool) ResponseValidatorOption {
	return func(v *ResponseValidator) {
		v.checkUnicode = check
	}
}

// NewResponseValidator creates a new response validator with given options
func NewResponseValidator(options ...ResponseValidatorOption) *ResponseValidator {
	validator := &ResponseValidator{
		minLength:    1, // Default minimum length
		trimSpaces:   true,
		checkUnicode: true,
	}

	for _, option := range options {
		option(validator)
	}

	return validator
}

// ValidateChatResponse validates a ChatResponse for emptiness and minimum requirements
func (v *ResponseValidator) ValidateChatResponse(resp *ChatResponse) *ValidationResult {
	if resp == nil {
		return &ValidationResult{
			IsValid: false,
			Reason:  "response is nil",
			Details: ValidationDetails{},
		}
	}

	return v.validateContent(resp.Content)
}

// ValidateVisionResponse validates a VisionResponse for emptiness and minimum requirements
func (v *ResponseValidator) ValidateVisionResponse(resp *VisionResponse) *ValidationResult {
	if resp == nil {
		return &ValidationResult{
			IsValid: false,
			Reason:  "response is nil",
			Details: ValidationDetails{},
		}
	}

	return v.validateContent(resp.Description)
}

// validateContent performs the actual content validation
func (v *ResponseValidator) validateContent(content string) *ValidationResult {
	// Initialize details
	details := ValidationDetails{
		ContentLength: len(content),
	}

	// Check if content is empty
	if content == "" {
		return &ValidationResult{
			IsValid: false,
			Reason:  "content is empty string",
			Details: details,
		}
	}

	// Trim spaces if configured
	trimmed := content
	if v.trimSpaces {
		trimmed = strings.TrimSpace(content)
		details.TrimmedLength = len(trimmed)
		details.HasWhitespace = trimmed != content
	}

	// Check if trimmed content is empty
	if trimmed == "" {
		return &ValidationResult{
			IsValid: false,
			Reason:  "content is only whitespace",
			Details: details,
		}
	}

	// Check minimum length requirement
	if len(trimmed) < v.minLength {
		details.MinLengthMet = false
		return &ValidationResult{
			IsValid: false,
			Reason:  "content too short",
			Details: details,
		}
	}

	// Check Unicode if enabled
	if v.checkUnicode && !v.hasValidUnicode(trimmed) {
		return &ValidationResult{
			IsValid: false,
			Reason:  "content contains only invalid Unicode",
			Details: details,
		}
	}

	// Content is valid
	details.HasContent = true
	details.MinLengthMet = true
	return &ValidationResult{
		IsValid: true,
		Reason:  "content is valid",
		Details: details,
	}
}

// hasValidUnicode checks if the string contains valid, printable Unicode characters
func (v *ResponseValidator) hasValidUnicode(s string) bool {
	for _, r := range s {
		// Check if character is a valid Unicode character (not replacement char)
		if r == unicode.ReplacementChar {
			return false
		}
		// Check if it's a control character (but allow common whitespace)
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return false
		}
		// Allow all other Unicode characters including emojis
	}
	return len(s) > 0 // Ensure we have at least one character
}

// IsEmptyResponse is a convenience method specifically for empty response detection
func (v *ResponseValidator) IsEmptyResponse(resp *ChatResponse) bool {
	result := v.ValidateChatResponse(resp)
	return !result.IsValid
}

// GetValidationReason returns a human-readable reason for validation failure
func (r *ValidationResult) GetValidationReason() string {
	if r.IsValid {
		return ""
	}
	return r.Reason
}

// String returns a string representation of the validation result
func (r *ValidationResult) String() string {
	if r.IsValid {
		return "valid"
	}
	return "invalid: " + r.Reason
}
