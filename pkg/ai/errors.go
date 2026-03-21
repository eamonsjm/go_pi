package ai

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	AuthMethod string // e.g., "oauth", "api-key" — empty if unknown
}

func (e *APIError) Error() string {
	prefix := e.Provider
	if prefix == "" {
		prefix = "api"
	}
	if e.AuthMethod != "" {
		prefix += "[" + e.AuthMethod + "]"
	}
	if e.ErrorType != "" {
		return fmt.Sprintf("%s: %s (HTTP %d): %s", prefix, e.ErrorType, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s: API error %d: %s", prefix, e.StatusCode, e.Message)
}

var promptTooLongRe = regexp.MustCompile(`(\d+)\s*tokens?\s*>\s*(\d+)\s*max`)

// isModelAccessError returns true if the error message indicates a model
// access/availability issue rather than an authentication problem.
func isModelAccessError(msg string) bool {
	lower := strings.ToLower(msg)
	hasModel := strings.Contains(lower, "model")
	if hasModel && (strings.Contains(lower, "not available") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "not have access") ||
		strings.Contains(lower, "not supported") ||
		strings.Contains(lower, "unavailable")) {
		return true
	}
	if strings.Contains(lower, "credit balance") {
		return true
	}
	return false
}

// UserMessage returns a user-friendly error message suitable for display in the TUI.
func (e *APIError) UserMessage() string {
	switch e.ErrorType {
	case "invalid_request_error":
		if m := promptTooLongRe.FindStringSubmatch(e.Message); m != nil {
			current := formatTokenCount(m[1])
			maximum := formatTokenCount(m[2])
			return fmt.Sprintf("Your conversation is too long (%s/%s tokens). Use /compact to shrink it.", current, maximum)
		}
		if isModelAccessError(e.Message) {
			return "Model not available on your current plan. Try a different model with /model."
		}
		return e.defaultUserMessage()

	case "not_found_error":
		if isModelAccessError(e.Message) {
			return "Model not available on your current plan. Try a different model with /model."
		}
		return e.defaultUserMessage()

	case "rate_limit_error":
		if e.RetryAfter > 0 {
			return fmt.Sprintf("Rate limited. Waiting %d seconds...", e.RetryAfter)
		}
		return "Rate limited. Please wait a moment and try again."

	case "overloaded_error":
		return "Anthropic servers are busy. Retrying..."

	case "authentication_error":
		if e.AuthMethod == "oauth" {
			return "OAuth token invalid or expired. Use /login to re-authenticate."
		}
		return "API key invalid. Check your key or use /login."

	case "permission_error":
		if e.AuthMethod == "oauth" {
			return "OAuth permission denied. Your token may lack required scopes. Use /login to re-authenticate."
		}
		return "Permission denied. Check your API key permissions."

	default:
		return e.defaultUserMessage()
	}
}

// defaultUserMessage builds a diagnostic message for unrecognized error types.
// It always includes provider, status code, and auth method so the user can
// diagnose the problem without having to grep logs.
func (e *APIError) defaultUserMessage() string {
	provider := e.Provider
	if provider == "" {
		provider = "API"
	}

	msg := e.Message
	if msg == "" {
		msg = "unknown error"
	}

	// Always include status code for unrecognized errors — the bare message
	// alone (e.g., "Error") is not diagnosable. Omit "HTTP 0" for SSE stream
	// errors where no status code is available.
	var base string
	if e.StatusCode > 0 {
		base = fmt.Sprintf("%s error (HTTP %d): %s", provider, e.StatusCode, msg)
	} else {
		base = fmt.Sprintf("%s error: %s", provider, msg)
	}

	if e.AuthMethod == "oauth" {
		if isModelAccessError(e.Message) {
			base += " [try a different model with /model]"
		} else {
			base += " [auth: OAuth — try /login to re-authenticate]"
		}
	}
	return base
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
