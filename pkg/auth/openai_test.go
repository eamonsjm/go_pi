package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOpenAIOAuth_Identity(t *testing.T) {
	o := NewOpenAIOAuth()
	if o.ID() != "openai" {
		t.Errorf("ID() = %q, want %q", o.ID(), "openai")
	}
	if o.Name() != "OpenAI (ChatGPT)" {
		t.Errorf("Name() = %q, want %q", o.Name(), "OpenAI (ChatGPT)")
	}
}

func TestOpenAIOAuth_Defaults(t *testing.T) {
	o := NewOpenAIOAuth()

	if o.AuthorizeURL != defaultOpenAIAuthorizeURL {
		t.Errorf("AuthorizeURL = %q, want %q", o.AuthorizeURL, defaultOpenAIAuthorizeURL)
	}
	if o.TokenURL != defaultOpenAITokenURL {
		t.Errorf("TokenURL = %q, want %q", o.TokenURL, defaultOpenAITokenURL)
	}
	if o.ClientID != defaultOpenAIClientID {
		t.Errorf("ClientID = %q, want %q", o.ClientID, defaultOpenAIClientID)
	}
	if o.RedirectURI != defaultOpenAIRedirectURI {
		t.Errorf("RedirectURI = %q, want %q", o.RedirectURI, defaultOpenAIRedirectURI)
	}
	if o.Scope != defaultOpenAIScope {
		t.Errorf("Scope = %q, want %q", o.Scope, defaultOpenAIScope)
	}
	if o.HTTPClient == nil {
		t.Error("HTTPClient is nil")
	}
}

func TestOpenAIOAuth_Options(t *testing.T) {
	customClient := &http.Client{Timeout: 10 * time.Second}
	o := NewOpenAIOAuth(
		WithOpenAIAuthorizeURL("https://custom-auth.example.com"),
		WithOpenAITokenURL("https://custom-token.example.com"),
		WithOpenAIClientID("custom-client-id"),
		WithOpenAIHTTPClient(customClient),
	)

	if o.AuthorizeURL != "https://custom-auth.example.com" {
		t.Errorf("AuthorizeURL = %q", o.AuthorizeURL)
	}
	if o.TokenURL != "https://custom-token.example.com" {
		t.Errorf("TokenURL = %q", o.TokenURL)
	}
	if o.ClientID != "custom-client-id" {
		t.Errorf("ClientID = %q", o.ClientID)
	}
	if o.HTTPClient != customClient {
		t.Error("HTTPClient not set by option")
	}
}

func TestOpenAIOAuth_GetAPIKey(t *testing.T) {
	o := NewOpenAIOAuth()
	cred := &Credential{
		Type:        CredentialOAuth,
		AccessToken: "bearer-token-xyz",
	}
	if got := o.GetAPIKey(cred); got != "bearer-token-xyz" {
		t.Errorf("GetAPIKey = %q, want %q", got, "bearer-token-xyz")
	}
}

func TestOpenAIOAuth_tokenToCredential(t *testing.T) {
	o := NewOpenAIOAuth()
	before := time.Now()
	token := &tokenResponse{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		ExpiresIn:    3600,
	}

	cred := o.tokenToCredential(token)
	after := time.Now()

	if cred.Type != CredentialOAuth {
		t.Errorf("Type = %q, want %q", cred.Type, CredentialOAuth)
	}
	if cred.AccessToken != "access-123" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if cred.RefreshToken != "refresh-456" {
		t.Errorf("RefreshToken = %q", cred.RefreshToken)
	}

	expectedMin := before.Add(3600 * time.Second).UnixMilli()
	expectedMax := after.Add(3600 * time.Second).UnixMilli()
	if cred.ExpiresAt < expectedMin || cred.ExpiresAt > expectedMax {
		t.Errorf("ExpiresAt = %d, want between %d and %d", cred.ExpiresAt, expectedMin, expectedMax)
	}
}

func TestOpenAIOAuth_tokenToCredential_ZeroExpiry(t *testing.T) {
	o := NewOpenAIOAuth()
	token := &tokenResponse{
		AccessToken: "access",
		ExpiresIn:   0,
	}

	cred := o.tokenToCredential(token)

	// With ExpiresIn=0, ExpiresAt should be approximately now.
	now := time.Now().UnixMilli()
	if abs(cred.ExpiresAt-now) > 1000 { // within 1 second
		t.Errorf("ExpiresAt = %d, expected close to %d", cred.ExpiresAt, now)
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- exchangeCode tests (uses httptest for mock token endpoint) ---

func TestOpenAIOAuth_exchangeCode_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
		}
		body, _ := io.ReadAll(r.Body)
		vals, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse form body: %v", err)
		}
		if vals.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q", vals.Get("grant_type"))
		}
		if vals.Get("code") != "test-code" {
			t.Errorf("code = %q", vals.Get("code"))
		}
		if vals.Get("code_verifier") != "test-verifier" {
			t.Errorf("code_verifier = %q", vals.Get("code_verifier"))
		}
		if vals.Get("client_id") == "" {
			t.Error("missing client_id")
		}
		if vals.Get("redirect_uri") == "" {
			t.Error("missing redirect_uri")
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "access-token-123",
			RefreshToken: "refresh-token-456",
			ExpiresIn:    7200,
		})
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	cred, err := o.exchangeCode(context.Background(), "test-code", "test-verifier")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if cred.AccessToken != "access-token-123" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if cred.RefreshToken != "refresh-token-456" {
		t.Errorf("RefreshToken = %q", cred.RefreshToken)
	}
	if cred.Type != CredentialOAuth {
		t.Errorf("Type = %q", cred.Type)
	}
}

func TestOpenAIOAuth_exchangeCode_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	_, err := o.exchangeCode(context.Background(), "bad-code", "verifier")
	if err == nil {
		t.Fatal("expected error")
	}
	var txErr *TokenExchangeError
	if !errors.As(err, &txErr) {
		t.Fatalf("expected TokenExchangeError, got %T: %v", err, err)
	}
	if txErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", txErr.StatusCode, http.StatusBadRequest)
	}
}

func TestOpenAIOAuth_exchangeCode_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json at all`))
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	_, err := o.exchangeCode(context.Background(), "code", "verifier")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse token response") {
		t.Errorf("error = %q, want to contain 'parse token response'", err.Error())
	}
}

func TestOpenAIOAuth_exchangeCode_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := o.exchangeCode(ctx, "code", "verifier")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// --- RefreshToken tests ---

func TestOpenAIOAuth_RefreshToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
		}
		body, _ := io.ReadAll(r.Body)
		vals, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse form body: %v", err)
		}
		if vals.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", vals.Get("grant_type"))
		}
		if vals.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh_token = %q", vals.Get("refresh_token"))
		}
		if vals.Get("client_id") == "" {
			t.Error("missing client_id")
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
			ExpiresIn:    7200,
		})
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	cred := &Credential{
		Type:         CredentialOAuth,
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).UnixMilli(),
	}

	refreshed, err := o.RefreshToken(context.Background(), cred)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if refreshed.AccessToken != "new-access" {
		t.Errorf("AccessToken = %q", refreshed.AccessToken)
	}
	if refreshed.RefreshToken != "new-refresh" {
		t.Errorf("RefreshToken = %q", refreshed.RefreshToken)
	}
}

func TestOpenAIOAuth_RefreshToken_KeepsOldRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: "new-access",
			ExpiresIn:   3600,
			// No new refresh token issued.
		})
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	cred := &Credential{
		Type:         CredentialOAuth,
		RefreshToken: "keep-this-refresh",
	}

	refreshed, err := o.RefreshToken(context.Background(), cred)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if refreshed.RefreshToken != "keep-this-refresh" {
		t.Errorf("RefreshToken = %q, want old token preserved", refreshed.RefreshToken)
	}
}

func TestOpenAIOAuth_RefreshToken_NoRefreshToken(t *testing.T) {
	o := NewOpenAIOAuth()
	_, err := o.RefreshToken(context.Background(), &Credential{Type: CredentialOAuth})
	if err == nil {
		t.Fatal("expected error when no refresh token")
	}
	if !strings.Contains(err.Error(), "no refresh token") {
		t.Errorf("error = %q, want to contain 'no refresh token'", err.Error())
	}
}

func TestOpenAIOAuth_RefreshToken_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	_, err := o.RefreshToken(context.Background(), &Credential{
		Type:         CredentialOAuth,
		RefreshToken: "expired-refresh",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var txErr *TokenExchangeError
	if !errors.As(err, &txErr) {
		t.Fatalf("expected TokenExchangeError, got %T", err)
	}
	if txErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", txErr.StatusCode)
	}
}

func TestOpenAIOAuth_RefreshToken_ErrorDetailFallback(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantDetail string
	}{
		{
			name:       "error_description present",
			body:       `{"error":"invalid_grant","error_description":"token revoked"}`,
			wantDetail: "token revoked",
		},
		{
			name:       "non-JSON body",
			body:       `Bad Request`,
			wantDetail: "Bad Request",
		},
		{
			name:       "empty body with JSON object",
			body:       `{"error":"server_error"}`,
			wantDetail: `{"error":"server_error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

			_, err := o.RefreshToken(context.Background(), &Credential{
				Type:         CredentialOAuth,
				RefreshToken: "some-token",
			})
			if err == nil {
				t.Fatal("expected error")
			}
			var txErr *TokenExchangeError
			if !errors.As(err, &txErr) {
				t.Fatalf("expected TokenExchangeError, got %T", err)
			}
			if txErr.Detail != tt.wantDetail {
				t.Errorf("Detail = %q, want %q", txErr.Detail, tt.wantDetail)
			}
		})
	}
}

func TestOpenAIOAuth_RefreshToken_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{not json}`))
	}))
	defer server.Close()

	o := NewOpenAIOAuth(WithOpenAITokenURL(server.URL))

	_, err := o.RefreshToken(context.Background(), &Credential{
		Type:         CredentialOAuth,
		RefreshToken: "token",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse refresh response") {
		t.Errorf("error = %q, want to contain 'parse refresh response'", err.Error())
	}
}

// --- Login tests (full OAuth flow with mock callback server) ---

// freePort returns a free TCP port on localhost by briefly listening and closing.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestOpenAIOAuth_Login_Success(t *testing.T) {
	// Mock token endpoint.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		if vals.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q", vals.Get("grant_type"))
		}
		if vals.Get("code") != "auth-code-from-browser" {
			t.Errorf("code = %q", vals.Get("code"))
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "login-access-token",
			RefreshToken: "login-refresh-token",
			ExpiresIn:    3600,
		})
	}))
	defer tokenServer.Close()

	port := freePort(t)
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)

	o := NewOpenAIOAuth(
		WithOpenAITokenURL(tokenServer.URL),
	)
	o.RedirectURI = redirectURI

	var authURL string
	var progressMsgs []string

	cred, err := o.Login(context.Background(), OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			authURL = u
			// Parse the authorize URL to extract the state parameter,
			// then simulate the browser callback with the correct state.
			parsed, err := url.Parse(u)
			if err != nil {
				t.Errorf("parse auth URL: %v", err)
				return
			}
			state := parsed.Query().Get("state")

			// Hit the callback server in a goroutine (server is already listening).
			go func() {
				callbackURL := fmt.Sprintf("%s?code=auth-code-from-browser&state=%s",
					redirectURI, url.QueryEscape(state))
				resp, err := http.Get(callbackURL)
				if err != nil {
					t.Errorf("callback request: %v", err)
					return
				}
				resp.Body.Close()
			}()
		},
		OnProgress: func(msg string) {
			progressMsgs = append(progressMsgs, msg)
		},
	})

	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if authURL == "" {
		t.Error("OnAuth was not called")
	}
	if cred.AccessToken != "login-access-token" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if cred.RefreshToken != "login-refresh-token" {
		t.Errorf("RefreshToken = %q", cred.RefreshToken)
	}
	if len(progressMsgs) < 2 {
		t.Errorf("expected at least 2 progress messages, got %d: %v", len(progressMsgs), progressMsgs)
	}

	// Verify authorize URL has expected parameters.
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("client_id") == "" {
		t.Error("missing client_id in authorize URL")
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("missing code_challenge in authorize URL")
	}
	if q.Get("state") == "" {
		t.Error("missing state in authorize URL")
	}
	if q.Get("redirect_uri") != redirectURI {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), redirectURI)
	}
}

func TestOpenAIOAuth_Login_OAuthError(t *testing.T) {
	port := freePort(t)
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)

	o := NewOpenAIOAuth()
	o.RedirectURI = redirectURI

	_, err := o.Login(context.Background(), OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			// Simulate the OAuth provider returning an error via callback.
			go func() {
				callbackURL := fmt.Sprintf("%s?error=access_denied&error_description=User+denied+access",
					redirectURI)
				resp, err := http.Get(callbackURL)
				if err != nil {
					t.Errorf("callback request: %v", err)
					return
				}
				resp.Body.Close()
			}()
		},
	})

	if err == nil {
		t.Fatal("expected error for OAuth error callback")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("error = %q, want to contain 'access_denied'", err.Error())
	}
}

func TestOpenAIOAuth_Login_StateMismatch(t *testing.T) {
	port := freePort(t)
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)

	o := NewOpenAIOAuth()
	o.RedirectURI = redirectURI

	_, err := o.Login(context.Background(), OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			// Send callback with wrong state parameter.
			go func() {
				callbackURL := fmt.Sprintf("%s?code=some-code&state=wrong-state",
					redirectURI)
				resp, err := http.Get(callbackURL)
				if err != nil {
					t.Errorf("callback request: %v", err)
					return
				}
				resp.Body.Close()
			}()
		},
	})

	if err == nil {
		t.Fatal("expected error for state mismatch")
	}
	if !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("error = %q, want to contain 'state mismatch'", err.Error())
	}
}

func TestOpenAIOAuth_Login_CallbackPath(t *testing.T) {
	// Verify Login works with a redirect URI that has no explicit path.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "path-test-access",
			RefreshToken: "path-test-refresh",
			ExpiresIn:    3600,
		})
	}))
	defer tokenServer.Close()

	port := freePort(t)
	// RedirectURI with no explicit path — the code defaults callbackPath to "/".
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d", port)

	o := NewOpenAIOAuth(WithOpenAITokenURL(tokenServer.URL))
	o.RedirectURI = redirectURI

	cred, err := o.Login(context.Background(), OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			parsed, _ := url.Parse(u)
			state := parsed.Query().Get("state")
			go func() {
				callbackURL := fmt.Sprintf("%s/?code=path-code&state=%s",
					redirectURI, url.QueryEscape(state))
				resp, err := http.Get(callbackURL)
				if err != nil {
					t.Errorf("callback request: %v", err)
					return
				}
				resp.Body.Close()
			}()
		},
	})

	if err != nil {
		t.Fatalf("Login with no-path redirect URI: %v", err)
	}
	if cred.AccessToken != "path-test-access" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
}

func TestOpenAIOAuth_Login_ExchangeCodeFailure(t *testing.T) {
	// Token endpoint that returns an error.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer tokenServer.Close()

	port := freePort(t)
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)

	o := NewOpenAIOAuth(WithOpenAITokenURL(tokenServer.URL))
	o.RedirectURI = redirectURI

	_, err := o.Login(context.Background(), OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			parsed, _ := url.Parse(u)
			state := parsed.Query().Get("state")
			go func() {
				callbackURL := fmt.Sprintf("%s?code=valid-code&state=%s",
					redirectURI, url.QueryEscape(state))
				resp, err := http.Get(callbackURL)
				if err != nil {
					t.Errorf("callback request: %v", err)
					return
				}
				resp.Body.Close()
			}()
		},
	})

	if err == nil {
		t.Fatal("expected error when token exchange fails")
	}
	var txErr *TokenExchangeError
	if !errors.As(err, &txErr) {
		t.Fatalf("expected TokenExchangeError, got %T: %v", err, err)
	}
}

func TestOpenAIOAuth_Login_NilOnProgressCallback(t *testing.T) {
	// Login should succeed without panicking when OnProgress is nil.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "nil-cb-token",
			RefreshToken: "nil-cb-refresh",
			ExpiresIn:    3600,
		})
	}))
	defer tokenServer.Close()

	port := freePort(t)
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)

	o := NewOpenAIOAuth(WithOpenAITokenURL(tokenServer.URL))
	o.RedirectURI = redirectURI

	cred, err := o.Login(context.Background(), OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			parsed, _ := url.Parse(u)
			state := parsed.Query().Get("state")
			go func() {
				callbackURL := fmt.Sprintf("%s?code=nil-cb-code&state=%s",
					redirectURI, url.QueryEscape(state))
				resp, err := http.Get(callbackURL)
				if err != nil {
					t.Errorf("callback request: %v", err)
					return
				}
				resp.Body.Close()
			}()
		},
		// OnProgress intentionally nil.
	})

	if err != nil {
		t.Fatalf("Login with nil OnProgress: %v", err)
	}
	if cred.AccessToken != "nil-cb-token" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
}
