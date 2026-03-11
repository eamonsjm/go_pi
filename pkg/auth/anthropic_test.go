package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestAnthropicOAuth_Identity(t *testing.T) {
	a := NewAnthropicOAuth()
	if a.ID() != "anthropic" {
		t.Errorf("ID() = %q, want %q", a.ID(), "anthropic")
	}
	if a.Name() != "Anthropic (Claude)" {
		t.Errorf("Name() = %q, want %q", a.Name(), "Anthropic (Claude)")
	}
}

func TestAnthropicOAuth_GetAPIKey(t *testing.T) {
	a := NewAnthropicOAuth()

	// Prefer Key field if set.
	cred := &Credential{
		Type:        CredentialOAuth,
		Key:         "api-key-from-oauth",
		AccessToken: "access-tok",
	}
	if got := a.GetAPIKey(cred); got != "api-key-from-oauth" {
		t.Errorf("GetAPIKey with Key = %q, want %q", got, "api-key-from-oauth")
	}

	// Fall back to AccessToken.
	cred.Key = ""
	if got := a.GetAPIKey(cred); got != "access-tok" {
		t.Errorf("GetAPIKey without Key = %q, want %q", got, "access-tok")
	}
}

func TestAnthropicOAuth_StartAuthFlow(t *testing.T) {
	a := NewAnthropicOAuth()
	a.AuthURL = "https://example.com"

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}
	defer session.Close()

	if session.AuthorizeURL == "" {
		t.Error("AuthorizeURL is empty")
	}
	if session.PKCE == nil || session.PKCE.Verifier == "" {
		t.Error("PKCE challenge not generated")
	}
	if session.State == "" {
		t.Error("state is empty")
	}
	if session.RedirectURI == "" {
		t.Error("RedirectURI is empty")
	}

	// Verify the authorization URL contains expected parameters.
	u, err := url.Parse(session.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse AuthorizeURL: %v", err)
	}
	if u.Host != "example.com" {
		t.Errorf("host = %q, want example.com", u.Host)
	}
	if u.Path != "/oauth/authorize" {
		t.Errorf("path = %q, want /oauth/authorize", u.Path)
	}
	q := u.Query()
	if q.Get("client_id") != a.ClientID {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("state") != session.State {
		t.Errorf("state mismatch in URL")
	}
}

func TestAnthropicOAuth_ExchangeAuthCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q, want authorization_code", r.Form.Get("grant_type"))
		}
		if r.Form.Get("code") != "test-auth-code" {
			t.Errorf("code = %q", r.Form.Get("code"))
		}
		if r.Form.Get("code_verifier") == "" {
			t.Error("missing code_verifier")
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "access-123",
			RefreshToken: "refresh-456",
			ExpiresIn:    3600,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	// Simulate browser callback by sending auth code to the session's callback server.
	go func() {
		callbackURL := session.RedirectURI + "?code=test-auth-code&state=" + session.State
		http.Get(callbackURL)
	}()

	cred, err := a.ExchangeAuthCode(session, 5*time.Second)
	if err != nil {
		t.Fatalf("ExchangeAuthCode: %v", err)
	}
	if cred.AccessToken != "access-123" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if cred.RefreshToken != "refresh-456" {
		t.Errorf("RefreshToken = %q", cred.RefreshToken)
	}
	if cred.Type != CredentialOAuth {
		t.Errorf("Type = %q, want oauth", cred.Type)
	}
}

func TestAnthropicOAuth_ExchangeAuthCode_APIKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: "access",
			APIKey:      "sk-ant-issued-key",
			ExpiresIn:   3600,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	go func() {
		callbackURL := session.RedirectURI + "?code=test-code&state=" + session.State
		http.Get(callbackURL)
	}()

	cred, err := a.ExchangeAuthCode(session, 5*time.Second)
	if err != nil {
		t.Fatalf("ExchangeAuthCode: %v", err)
	}
	if cred.Key != "sk-ant-issued-key" {
		t.Errorf("Key = %q, want %q", cred.Key, "sk-ant-issued-key")
	}
	if got := a.GetAPIKey(cred); got != "sk-ant-issued-key" {
		t.Errorf("GetAPIKey = %q, want %q", got, "sk-ant-issued-key")
	}
}

func TestAnthropicOAuth_ExchangeAuthCode_ErrorCallback(t *testing.T) {
	a := NewAnthropicOAuth()

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	// Simulate an error callback from the OAuth provider.
	go func() {
		callbackURL := session.RedirectURI + "?error=access_denied&error_description=user+denied"
		http.Get(callbackURL)
	}()

	_, err = a.ExchangeAuthCode(session, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for access denied callback")
	}
	if got := err.Error(); got != "authorization error: user denied" {
		t.Errorf("error = %q", got)
	}
}

func TestAnthropicOAuth_ExchangeAuthCode_StateMismatch(t *testing.T) {
	a := NewAnthropicOAuth()

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	// Send callback with wrong state.
	go func() {
		callbackURL := session.RedirectURI + "?code=test-code&state=wrong-state"
		http.Get(callbackURL)
	}()

	_, err = a.ExchangeAuthCode(session, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for state mismatch")
	}
	if got := err.Error(); got != "state mismatch" {
		t.Errorf("error = %q", got)
	}
}

func TestAnthropicOAuth_ExchangeAuthCode_Timeout(t *testing.T) {
	a := NewAnthropicOAuth()

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	// Don't send any callback — should timeout.
	_, err = a.ExchangeAuthCode(session, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAnthropicOAuth_RefreshToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
			ExpiresIn:    7200,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	cred := &Credential{
		Type:         CredentialOAuth,
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).UnixMilli(),
	}

	refreshed, err := a.RefreshToken(cred)
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

func TestAnthropicOAuth_RefreshToken_KeepsOldRefresh(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: "new-access",
			// No new refresh token issued.
			ExpiresIn: 3600,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	cred := &Credential{
		Type:         CredentialOAuth,
		RefreshToken: "keep-this-refresh",
	}

	refreshed, err := a.RefreshToken(cred)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if refreshed.RefreshToken != "keep-this-refresh" {
		t.Errorf("RefreshToken = %q, want old token preserved", refreshed.RefreshToken)
	}
}

func TestAnthropicOAuth_RefreshToken_NoRefreshToken(t *testing.T) {
	a := NewAnthropicOAuth()
	_, err := a.RefreshToken(&Credential{Type: CredentialOAuth})
	if err == nil {
		t.Fatal("expected error when no refresh token")
	}
}

func TestAnthropicOAuth_Login_FullFlow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "login-access",
			RefreshToken: "login-refresh",
			ExpiresIn:    3600,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	var authURL, authInstructions string
	var progressMsgs []string

	// Login runs StartAuthFlow internally, so we need to simulate the
	// browser callback. We capture the auth URL from OnAuth, then send
	// a callback to the local server.
	cred, err := a.Login(OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			authURL = u
			authInstructions = instructions

			// Parse the redirect_uri from the authorize URL to find the callback server.
			parsed, _ := url.Parse(u)
			redirectURI := parsed.Query().Get("redirect_uri")
			state := parsed.Query().Get("state")

			// Simulate browser redirect.
			go func() {
				http.Get(redirectURI + "?code=login-code&state=" + state)
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
		t.Error("OnAuth URL was not called")
	}
	if authInstructions != "Open this URL in your browser to authorize" {
		t.Errorf("OnAuth instructions = %q", authInstructions)
	}
	if cred.AccessToken != "login-access" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if len(progressMsgs) < 2 {
		t.Errorf("expected at least 2 progress messages, got %d", len(progressMsgs))
	}
}
