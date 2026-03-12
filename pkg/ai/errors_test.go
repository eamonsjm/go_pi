package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIError_UserMessage(t *testing.T) {
	tests := []struct {
		name     string
		err      *APIError
		contains string
	}{
		{
			name:     "prompt too long",
			err:      &APIError{ErrorType: "invalid_request_error", Message: "prompt is too long: 213568 tokens > 200000 maximum"},
			contains: "too long (213k/200k tokens)",
		},
		{
			name:     "prompt too long with 'token' singular",
			err:      &APIError{ErrorType: "invalid_request_error", Message: "213568 token > 200000 max"},
			contains: "too long (213k/200k tokens)",
		},
		{
			name:     "other invalid request",
			err:      &APIError{ErrorType: "invalid_request_error", Message: "messages: roles must alternate"},
			contains: "roles must alternate",
		},
		{
			name:     "rate limit with retry-after",
			err:      &APIError{ErrorType: "rate_limit_error", Message: "rate limited", RetryAfter: 30},
			contains: "Rate limited. Waiting 30 seconds",
		},
		{
			name:     "rate limit no retry-after",
			err:      &APIError{ErrorType: "rate_limit_error", Message: "rate limited"},
			contains: "Rate limited. Please wait",
		},
		{
			name:     "overloaded",
			err:      &APIError{ErrorType: "overloaded_error", Message: "API is overloaded"},
			contains: "servers are busy",
		},
		{
			name:     "authentication",
			err:      &APIError{ErrorType: "authentication_error", Message: "invalid api key"},
			contains: "API key invalid",
		},
		{
			name:     "authentication suggests login",
			err:      &APIError{ErrorType: "authentication_error", Message: "invalid api key"},
			contains: "/login",
		},
		{
			name:     "permission",
			err:      &APIError{ErrorType: "permission_error", Message: "not allowed"},
			contains: "Permission denied",
		},
		{
			name:     "unknown error type with message",
			err:      &APIError{ErrorType: "new_error_type", Message: "something went wrong"},
			contains: "something went wrong",
		},
		{
			name:     "unknown error type no message",
			err:      &APIError{StatusCode: 500, ErrorType: ""},
			contains: "API error (status 500)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.UserMessage()
			if !strings.Contains(msg, tt.contains) {
				t.Errorf("UserMessage() = %q, want substring %q", msg, tt.contains)
			}
		})
	}
}

func TestAPIError_IsRetryable(t *testing.T) {
	tests := []struct {
		errType   string
		retryable bool
	}{
		{"rate_limit_error", true},
		{"overloaded_error", true},
		{"authentication_error", false},
		{"invalid_request_error", false},
		{"api_error", false},
	}

	for _, tt := range tests {
		t.Run(tt.errType, func(t *testing.T) {
			err := &APIError{ErrorType: tt.errType}
			if got := err.IsRetryable(); got != tt.retryable {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestAPIError_Error(t *testing.T) {
	err := &APIError{ErrorType: "invalid_request_error", Message: "bad request"}
	if !strings.Contains(err.Error(), "invalid_request_error") {
		t.Errorf("Error() = %q, want to contain error type", err.Error())
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Errorf("Error() = %q, want to contain message", err.Error())
	}

	// Without error type, should show status code.
	err2 := &APIError{StatusCode: 500, Message: "internal"}
	if !strings.Contains(err2.Error(), "500") {
		t.Errorf("Error() = %q, want to contain status code", err2.Error())
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"213568", "213k"},
		{"200000", "200k"},
		{"500", "500"},
		{"1000", "1k"},
		{"999", "999"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := formatTokenCount(tt.input); got != tt.want {
				t.Errorf("formatTokenCount(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAnthropicStream_ParsedHTTPErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		headers    map[string]string
		errType    string
		retryable  bool
		msgContain string
	}{
		{
			name:       "authentication error",
			status:     401,
			body:       `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			errType:    "authentication_error",
			retryable:  false,
			msgContain: "API key invalid",
		},
		{
			name:       "invalid request prompt too long",
			status:     400,
			body:       `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 213568 tokens > 200000 maximum"}}`,
			errType:    "invalid_request_error",
			retryable:  false,
			msgContain: "too long",
		},
		{
			name:       "rate limit with retry-after header",
			status:     429,
			body:       `{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`,
			headers:    map[string]string{"Retry-After": "15"},
			errType:    "rate_limit_error",
			retryable:  true,
			msgContain: "Rate limited",
		},
		{
			name:       "overloaded",
			status:     529,
			body:       `{"type":"error","error":{"type":"overloaded_error","message":"API is overloaded"}}`,
			errType:    "overloaded_error",
			retryable:  true,
			msgContain: "servers are busy",
		},
		{
			name:       "malformed error body fallback",
			status:     500,
			body:       `not json at all`,
			errType:    "",
			retryable:  false,
			msgContain: "not json at all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			p := &AnthropicProvider{
				apiKey:     "test-key",
				httpClient: srv.Client(),
				baseURL:    srv.URL,
			}

			_, err := p.Stream(context.Background(), StreamRequest{
				Model:    "test",
				Messages: []Message{NewTextMessage(RoleUser, "hi")},
			})
			if err == nil {
				t.Fatal("expected error")
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *APIError, got %T: %v", err, err)
			}

			if tt.errType != "" && apiErr.ErrorType != tt.errType {
				t.Errorf("ErrorType = %q, want %q", apiErr.ErrorType, tt.errType)
			}
			if apiErr.IsRetryable() != tt.retryable {
				t.Errorf("IsRetryable() = %v, want %v", apiErr.IsRetryable(), tt.retryable)
			}
			msg := apiErr.UserMessage()
			if !strings.Contains(msg, tt.msgContain) {
				t.Errorf("UserMessage() = %q, want substring %q", msg, tt.msgContain)
			}
		})
	}
}

func TestAnthropicStream_SSEErrorParsedAsAPIError(t *testing.T) {
	sse := `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"API is overloaded"}}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	if len(events) != 1 || events[0].Type != EventError {
		t.Fatalf("expected 1 error event, got %d events", len(events))
	}

	var apiErr *APIError
	if !errors.As(events[0].Error, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", events[0].Error, events[0].Error)
	}
	if apiErr.ErrorType != "overloaded_error" {
		t.Errorf("ErrorType = %q, want overloaded_error", apiErr.ErrorType)
	}
	msg := apiErr.UserMessage()
	if !strings.Contains(msg, "servers are busy") {
		t.Errorf("UserMessage() = %q, want substring 'servers are busy'", msg)
	}
}

func TestAnthropicStream_RetryOnOverloaded(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(529)
			fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"API is overloaded"}}`)
			return
		}
		// Third attempt succeeds.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"+
			"event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"+
			"event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
			"event: message_delta\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"+
			"event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream should succeed after retry: %v", err)
	}

	events := collectEvents(ch)
	var text string
	for _, e := range events {
		if e.Type == EventTextDelta {
			text += e.Delta
		}
	}
	if text != "ok" {
		t.Errorf("expected text %q after retry, got %q", "ok", text)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestAnthropicStream_RetryExhausted(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(529)
		fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"API is overloaded"}}`)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.ErrorType != "overloaded_error" {
		t.Errorf("ErrorType = %q, want overloaded_error", apiErr.ErrorType)
	}
	// Should have attempted maxRetries+1 times.
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestAnthropicStream_NoRetryOnNonRetryable(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(401)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for auth errors), got %d", attempts)
	}
}
