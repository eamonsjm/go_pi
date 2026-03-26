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
			name:     "other invalid request includes context",
			err:      &APIError{ErrorType: "invalid_request_error", Message: "messages: roles must alternate", StatusCode: 400, Provider: "anthropic"},
			contains: "HTTP 400",
		},
		{
			name:     "other invalid request includes message",
			err:      &APIError{ErrorType: "invalid_request_error", Message: "messages: roles must alternate", StatusCode: 400, Provider: "anthropic"},
			contains: "roles must alternate",
		},
		{
			name:     "invalid request bare Error includes provider",
			err:      &APIError{ErrorType: "invalid_request_error", Message: "Error", StatusCode: 400, Provider: "anthropic"},
			contains: "anthropic error",
		},
		{
			name:     "invalid request no status code omits HTTP 0",
			err:      &APIError{ErrorType: "invalid_request_error", Message: "Error", Provider: "anthropic"},
			contains: "anthropic error: Error",
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
			name:     "authentication oauth",
			err:      &APIError{ErrorType: "authentication_error", Message: "invalid token", AuthMethod: "oauth"},
			contains: "OAuth token invalid",
		},
		{
			name:     "permission",
			err:      &APIError{ErrorType: "permission_error", Message: "not allowed"},
			contains: "Permission denied",
		},
		{
			name:     "permission oauth",
			err:      &APIError{ErrorType: "permission_error", Message: "not allowed", AuthMethod: "oauth"},
			contains: "OAuth permission denied",
		},
		{
			name:     "unknown error type with message includes status",
			err:      &APIError{StatusCode: 403, ErrorType: "new_error_type", Message: "something went wrong", Provider: "anthropic"},
			contains: "HTTP 403",
		},
		{
			name:     "unknown error type no message",
			err:      &APIError{StatusCode: 500, ErrorType: "", Provider: "anthropic"},
			contains: "HTTP 500",
		},
		{
			name:     "unknown error oauth includes auth method and re-auth hint",
			err:      &APIError{StatusCode: 400, Message: "Error", Provider: "anthropic", AuthMethod: "oauth"},
			contains: "auth: OAuth — try /login to re-authenticate",
		},
		{
			name:     "opaque oauth error includes HTTP status",
			err:      &APIError{StatusCode: 403, ErrorType: "Error", Message: "OAuth request failed (HTTP 403)", Provider: "anthropic", AuthMethod: "oauth"},
			contains: "HTTP 403",
		},
		{
			name:     "model access error oauth does not suggest login",
			err:      &APIError{ErrorType: "invalid_request_error", StatusCode: 400, Message: "model: claude-3-opus-20240229 does not exist or you do not have access to it", Provider: "anthropic", AuthMethod: "oauth"},
			contains: "Try a different model with /model",
		},
		{
			name:     "model not available on plan",
			err:      &APIError{ErrorType: "invalid_request_error", StatusCode: 400, Message: "The model claude-3-opus is not available on your plan", Provider: "anthropic", AuthMethod: "oauth"},
			contains: "not available on your current plan",
		},
		{
			name:     "model not found error",
			err:      &APIError{ErrorType: "not_found_error", StatusCode: 404, Message: "model: claude-3-opus-20240229 not found", Provider: "anthropic", AuthMethod: "oauth"},
			contains: "Try a different model with /model",
		},
		{
			name:     "credit balance model access",
			err:      &APIError{ErrorType: "invalid_request_error", StatusCode: 400, Message: "Your credit balance is too low to access claude-3-opus", Provider: "anthropic", AuthMethod: "oauth"},
			contains: "not available on your current plan",
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

func TestAPIError_ModelAccessDoesNotSuggestLogin(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
	}{
		{
			name: "invalid_request model access oauth",
			err:  &APIError{ErrorType: "invalid_request_error", StatusCode: 400, Message: "model: claude-3-opus does not exist or you do not have access to it", Provider: "anthropic", AuthMethod: "oauth"},
		},
		{
			name: "not_found model access oauth",
			err:  &APIError{ErrorType: "not_found_error", StatusCode: 404, Message: "model: claude-3-opus not found", Provider: "anthropic", AuthMethod: "oauth"},
		},
		{
			name: "credit balance oauth",
			err:  &APIError{ErrorType: "invalid_request_error", StatusCode: 400, Message: "Your credit balance is too low to access claude-3-opus", Provider: "anthropic", AuthMethod: "oauth"},
		},
		{
			name: "model not available oauth",
			err:  &APIError{ErrorType: "invalid_request_error", StatusCode: 400, Message: "The model is not available on your plan", Provider: "anthropic", AuthMethod: "oauth"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.UserMessage()
			if strings.Contains(msg, "/login") {
				t.Errorf("UserMessage() = %q, should NOT contain /login for model access errors", msg)
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
	err := &APIError{ErrorType: "invalid_request_error", Message: "bad request", Provider: "anthropic", StatusCode: 400}
	if got := err.Error(); got != "anthropic: invalid_request_error (HTTP 400): bad request" {
		t.Errorf("Error() = %q, want %q", got, "anthropic: invalid_request_error (HTTP 400): bad request")
	}

	// With OAuth auth method.
	errOAuth := &APIError{ErrorType: "authentication_error", Message: "invalid token", Provider: "anthropic", StatusCode: 401, AuthMethod: "oauth"}
	if got := errOAuth.Error(); got != "anthropic[oauth]: authentication_error (HTTP 401): invalid token" {
		t.Errorf("Error() = %q, want %q", got, "anthropic[oauth]: authentication_error (HTTP 401): invalid token")
	}

	// Gemini provider should show "gemini:" prefix.
	errG := &APIError{StatusCode: 429, Message: "quota exceeded", Provider: "gemini"}
	if got := errG.Error(); got != "gemini: API error 429: quota exceeded" {
		t.Errorf("Error() = %q, want %q", got, "gemini: API error 429: quota exceeded")
	}

	// Without provider, should fall back to "api:" prefix.
	err2 := &APIError{StatusCode: 500, Message: "internal"}
	if got := err2.Error(); got != "api: API error 500: internal" {
		t.Errorf("Error() = %q, want %q", got, "api: API error 500: internal")
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
		{
			name:       "model access 400 surfaces model error not auth",
			status:     400,
			body:       `{"type":"error","error":{"type":"invalid_request_error","message":"model: claude-3-opus-20240229 does not exist or you do not have access to it"}}`,
			errType:    "invalid_request_error",
			retryable:  false,
			msgContain: "Try a different model with /model",
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
	if apiErr.AuthMethod != "api-key" {
		t.Errorf("AuthMethod = %q, want api-key", apiErr.AuthMethod)
	}
	msg := apiErr.UserMessage()
	if !strings.Contains(msg, "servers are busy") {
		t.Errorf("UserMessage() = %q, want substring 'servers are busy'", msg)
	}
}

func TestAnthropicStream_SSEErrorOAuthIncludesAuthMethod(t *testing.T) {
	sse := `event: error
data: {"type":"error","error":{"type":"invalid_request_error","message":"Error"}}

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sse)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		apiKey:     "oauth-token",
		useBearer:  true,
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:        "test",
		SystemPrompt: "test",
		Messages:     []Message{NewTextMessage(RoleUser, "hi")},
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
	if apiErr.AuthMethod != "oauth" {
		t.Errorf("AuthMethod = %q, want oauth", apiErr.AuthMethod)
	}

	// With the fix, a bare "Error" message from invalid_request_error must
	// include provider context instead of just returning "Error".
	msg := apiErr.UserMessage()
	if !strings.Contains(msg, "anthropic") {
		t.Errorf("UserMessage() = %q, want to contain 'anthropic'", msg)
	}
	if !strings.Contains(msg, "OAuth") {
		t.Errorf("UserMessage() = %q, want to contain 'OAuth'", msg)
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

// --- OpenAI retry tests ---

func TestOpenAIStream_RetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`)
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestOpenAIStream_RetryOn503(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"Service unavailable","type":"server_error"}}`)
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestOpenAIStream_NoRetryOn401(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid key","type":"authentication_error"}}`)
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for auth errors), got %d", attempts)
	}
}

// --- Azure retry tests ---

func TestAzureStream_RetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`)
	}))
	defer srv.Close()

	client := srv.Client()
	p := &AzureOpenAIProvider{
		apiKey:     "test-key",
		endpoint:   srv.URL,
		deployment: "gpt-4o",
		apiVersion: "2024-10-21",
		httpClient: client,
		inner:      &OpenAIProvider{apiKey: "test-key", httpClient: client},
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestAzureStream_NoRetryOn401(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid key","type":"authentication_error"}}`)
	}))
	defer srv.Close()

	client := srv.Client()
	p := &AzureOpenAIProvider{
		apiKey:     "test-key",
		endpoint:   srv.URL,
		deployment: "gpt-4o",
		apiVersion: "2024-10-21",
		httpClient: client,
		inner:      &OpenAIProvider{apiKey: "test-key", httpClient: client},
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for auth errors), got %d", attempts)
	}
}

// --- Ollama retry tests ---

func TestOllamaStream_RetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()

	p := &OllamaProvider{
		baseURL:    srv.URL,
		httpClient: srv.Client(),
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "llama3",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestOllamaStream_RetryOn503(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"service unavailable"}`)
	}))
	defer srv.Close()

	p := &OllamaProvider{
		baseURL:    srv.URL,
		httpClient: srv.Client(),
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "llama3",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

// --- Gemini retry tests ---

func TestGeminiStream_RetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`)
	}))
	defer srv.Close()

	p := &GeminiProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestGeminiStream_RetryRespectsRetryAfterHeader(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`)
	}))
	defer srv.Close()

	p := &GeminiProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.RetryAfter != 1 {
		t.Errorf("expected RetryAfter=1, got %d", apiErr.RetryAfter)
	}
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestGeminiStream_NoRetryOn401(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":401,"message":"API key invalid","status":"UNAUTHENTICATED"}}`)
	}))
	defer srv.Close()

	p := &GeminiProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for auth errors), got %d", attempts)
	}
}

func TestOllamaStream_NoRetryOn404(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model not found"}`)
	}))
	defer srv.Close()

	p := &OllamaProvider{
		baseURL:    srv.URL,
		httpClient: srv.Client(),
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "llama3",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry for 404), got %d", attempts)
	}
}
