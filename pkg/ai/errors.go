package ai

import (
	"fmt"
	"regexp"
	"strconv"
)

// APIError represents a parsed error response from an AI provider's API.
// It extracts structured information from the error JSON so callers can
// display user-friendly messages and implement retry logic.
type APIError struct {
	StatusCode int
	ErrorType  string // e.g., "invalid_request_error", "rate_limit_error"
	Message    string // The raw error message from the API
	RetryAfter int    // Seconds to wait before retrying (0 if not set)
	Provider   string // e.g., "anthropic", "gemini"
}

func (e *APIError) Error() string {
	prefix := e.Provider
	if prefix == "" {
		prefix = "api"
	}
	if e.ErrorType != "" {
		return fmt.Sprintf("%s: %s: %s", prefix, e.ErrorType, e.Message)
	}
	return fmt.Sprintf("%s: API error %d: %s", prefix, e.StatusCode, e.Message)
}

var promptTooLongRe = regexp.MustCompile(`(\d+)\s*tokens?\s*>\s*(\d+)\s*max`)

// UserMessage returns a user-friendly error message suitable for display in the TUI.
func (e *APIError) UserMessage() string {
	switch e.ErrorType {
	case "invalid_request_error":
		if m := promptTooLongRe.FindStringSubmatch(e.Message); m != nil {
			current := formatTokenCount(m[1])
			maximum := formatTokenCount(m[2])
			return fmt.Sprintf("Your conversation is too long (%s/%s tokens). Use /compact to shrink it.", current, maximum)
		}
		return e.Message

	case "rate_limit_error":
		if e.RetryAfter > 0 {
			return fmt.Sprintf("Rate limited. Waiting %d seconds...", e.RetryAfter)
		}
		return "Rate limited. Please wait a moment and try again."

	case "overloaded_error":
		return "Anthropic servers are busy. Retrying..."

	case "authentication_error":
		return "API key invalid. Check your key or use /login."

	case "permission_error":
		return "Permission denied. Check your API key permissions."

	default:
		if e.Message != "" {
			return e.Message
		}
		return fmt.Sprintf("API error (status %d)", e.StatusCode)
	}
}

// IsRetryable returns true if this error type can be retried.
func (e *APIError) IsRetryable() bool {
	return e.ErrorType == "rate_limit_error" || e.ErrorType == "overloaded_error"
}

// formatTokenCount formats a token count string like "213568" as "213k".
func formatTokenCount(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil {
		return s
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return s
}
