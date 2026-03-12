package auth

import (
	"encoding/json"
	"io"
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

func TestAnthropicOAuth_Defaults(t *testing.T) {
	a := NewAnthropicOAuth()

	if a.ClientID != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Errorf("ClientID = %q", a.ClientID)
	}
	if a.AuthorizeURL != "https://claude.ai" {
		t.Errorf("AuthorizeURL = %q", a.AuthorizeURL)
	}
	if a.TokenURL != "https://console.anthropic.com" {
		t.Errorf("TokenURL = %q", a.TokenURL)
	}
	if a.RedirectURI != "https://console.anthropic.com/oauth/code/callback" {
		t.Errorf("RedirectURI = %q", a.RedirectURI)
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
	a.AuthorizeURL = "https://example.com"

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	if session.AuthorizeURL == "" {
		t.Error("AuthorizeURL is empty")
	}
	if session.PKCE == nil || session.PKCE.Verifier == "" {
		t.Error("PKCE challenge not generated")
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
	if q.Get("code") != "true" {
		t.Errorf("code param = %q, want true", q.Get("code"))
	}
	// State should equal the PKCE verifier.
	if q.Get("state") != session.PKCE.Verifier {
		t.Errorf("state = %q, want PKCE verifier %q", q.Get("state"), session.PKCE.Verifier)
	}
	if q.Get("redirect_uri") != defaultAnthropicRedirectURI {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

func TestAnthropicOAuth_ExchangeCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		var req tokenExchangeRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.GrantType != "authorization_code" {
			t.Errorf("grant_type = %q, want authorization_code", req.GrantType)
		}
		if req.Code != "test-auth-code" {
			t.Errorf("code = %q", req.Code)
		}
		if req.CodeVerifier == "" {
			t.Error("missing code_verifier")
		}
		if req.State == "" {
			t.Error("missing state")
		}
		// State should equal code_verifier.
		if req.State != req.CodeVerifier {
			t.Errorf("state %q != code_verifier %q", req.State, req.CodeVerifier)
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
	a.TokenURL = server.URL

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	cred, err := a.ExchangeCode(session, "test-auth-code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
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

func TestAnthropicOAuth_ExchangeCode_APIKey(t *testing.T) {
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
	a.TokenURL = server.URL

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	cred, err := a.ExchangeCode(session, "test-code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if cred.Key != "sk-ant-issued-key" {
		t.Errorf("Key = %q, want %q", cred.Key, "sk-ant-issued-key")
	}
	if got := a.GetAPIKey(cred); got != "sk-ant-issued-key" {
		t.Errorf("GetAPIKey = %q, want %q", got, "sk-ant-issued-key")
	}
}

func TestAnthropicOAuth_ExchangeCode_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.TokenURL = server.URL

	session, err := a.StartAuthFlow()
	if err != nil {
		t.Fatalf("StartAuthFlow: %v", err)
	}

	_, err = a.ExchangeCode(session, "bad-code")
	if err == nil {
		t.Fatal("expected error for bad code")
	}
}

func TestAnthropicOAuth_RefreshToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		var req tokenExchangeRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.GrantType != "refresh_token" {
			t.Errorf("grant_type = %q", req.GrantType)
		}
		if req.RefreshToken != "old-refresh" {
			t.Errorf("refresh_token = %q", req.RefreshToken)
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
	a.TokenURL = server.URL

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
	a.TokenURL = server.URL

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
	a.TokenURL = server.URL

	var authURL, authInstructions string
	var progressMsgs []string

	cred, err := a.Login(OAuthCallbacks{
		OnAuth: func(u, instructions string) {
			authURL = u
			authInstructions = instructions
		},
		OnPrompt: func(prompt string) (string, error) {
			// Simulate user pasting the code.
			return "login-code", nil
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
	if authInstructions == "" {
		t.Error("OnAuth instructions was empty")
	}
	if cred.AccessToken != "login-access" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if len(progressMsgs) < 2 {
		t.Errorf("expected at least 2 progress messages, got %d", len(progressMsgs))
	}
}

func TestAnthropicOAuth_Login_NoPromptCallback(t *testing.T) {
	a := NewAnthropicOAuth()
	_, err := a.Login(OAuthCallbacks{})
	if err == nil {
		t.Fatal("expected error when no OnPrompt callback")
	}
}

func TestAnthropicOAuth_Login_EmptyCode(t *testing.T) {
	a := NewAnthropicOAuth()
	_, err := a.Login(OAuthCallbacks{
		OnPrompt: func(prompt string) (string, error) {
			return "", nil
		},
	})
	if err == nil {
		t.Fatal("expected error for empty code")
	}
}
