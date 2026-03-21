package auth

import "fmt"

// TokenExchangeError is returned when the OAuth token endpoint responds
// with a non-200 status code during token exchange or refresh.
// Callers can inspect StatusCode and Detail programmatically via errors.As.
type TokenExchangeError struct {
	// Operation describes what was attempted (e.g. "token exchange", "refresh").
	Operation string
	// StatusCode is the HTTP status code from the token endpoint.
	StatusCode int
	// Detail is the human-readable error detail from the response body.
	Detail string
}

func (e *TokenExchangeError) Error() string {
	return fmt.Sprintf("%s failed (%d): %s", e.Operation, e.StatusCode, e.Detail)
}
