package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestAnthropicOAuth_RequestDeviceCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/authorize/device", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.Form.Get("client_id") == "" {
			t.Error("missing client_id")
		}
		if r.Form.Get("code_challenge") == "" {
			t.Error("missing code_challenge")
		}
		if r.Form.Get("code_challenge_method") != "S256" {
			t.Errorf("code_challenge_method = %q, want S256", r.Form.Get("code_challenge_method"))
		}

		json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:      "dev-code-123",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://example.com/activate",
			ExpiresIn:       300,
			Interval:        5,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	resp, pkce, err := a.RequestDeviceCode()
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if resp.DeviceCode != "dev-code-123" {
		t.Errorf("DeviceCode = %q", resp.DeviceCode)
	}
	if resp.UserCode != "ABCD-1234" {
		t.Errorf("UserCode = %q", resp.UserCode)
	}
	if pkce == nil || pkce.Verifier == "" {
		t.Error("PKCE challenge not generated")
	}
}

func TestAnthropicOAuth_ExchangeDeviceCode_Immediate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Errorf("unexpected grant_type: %s", r.Form.Get("grant_type"))
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

	cred, err := a.ExchangeDeviceCode("dev-code", "verifier",
		100*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode: %v", err)
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

func TestAnthropicOAuth_ExchangeDeviceCode_PendingThenSuccess(t *testing.T) {
	var callCount atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(tokenErrorResponse{
				Error:            "authorization_pending",
				ErrorDescription: "user hasn't authorized yet",
			})
			return
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "delayed-access",
			RefreshToken: "delayed-refresh",
			ExpiresIn:    3600,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	cred, err := a.ExchangeDeviceCode("dev-code", "verifier",
		50*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode: %v", err)
	}
	if cred.AccessToken != "delayed-access" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 poll attempts, got %d", callCount.Load())
	}
}

func TestAnthropicOAuth_ExchangeDeviceCode_AccessDenied(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(tokenErrorResponse{
			Error:            "access_denied",
			ErrorDescription: "user denied the request",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	_, err := a.ExchangeDeviceCode("dev-code", "verifier",
		50*time.Millisecond, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for access denied")
	}
	if got := err.Error(); got != "authorization denied by user" {
		t.Errorf("error = %q", got)
	}
}

func TestAnthropicOAuth_ExchangeDeviceCode_SlowDown(t *testing.T) {
	var callCount atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(tokenErrorResponse{
				Error: "slow_down",
			})
			return
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: "slow-access",
			ExpiresIn:   3600,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	a := NewAnthropicOAuth()
	a.AuthURL = server.URL

	cred, err := a.ExchangeDeviceCode("dev-code", "verifier",
		50*time.Millisecond, 30*time.Second)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode: %v", err)
	}
	if cred.AccessToken != "slow-access" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
}

func TestAnthropicOAuth_ExchangeDeviceCode_APIKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
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

	cred, err := a.ExchangeDeviceCode("dev-code", "verifier",
		50*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode: %v", err)
	}
	if cred.Key != "sk-ant-issued-key" {
		t.Errorf("Key = %q, want %q", cred.Key, "sk-ant-issued-key")
	}
	// GetAPIKey should prefer the Key field.
	if got := a.GetAPIKey(cred); got != "sk-ant-issued-key" {
		t.Errorf("GetAPIKey = %q, want %q", got, "sk-ant-issued-key")
	}
}

func TestAnthropicOAuth_RefreshToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
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
	var callCount atomic.Int32
	mux := http.NewServeMux()

	mux.HandleFunc("/oauth/authorize/device", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:      "login-dev-code",
			UserCode:        "TEST-CODE",
			VerificationURI: "https://example.com/activate",
			ExpiresIn:       300,
			Interval:        1,
		})
	})

	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(tokenErrorResponse{
				Error: "authorization_pending",
			})
			return
		}
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

	cred, err := a.Login(OAuthCallbacks{
		OnAuth: func(url, instructions string) {
			authURL = url
			authInstructions = instructions
		},
		OnProgress: func(msg string) {
			progressMsgs = append(progressMsgs, msg)
		},
	})

	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if authURL != "https://example.com/activate" {
		t.Errorf("OnAuth URL = %q", authURL)
	}
	if authInstructions != "Enter the code: TEST-CODE" {
		t.Errorf("OnAuth instructions = %q", authInstructions)
	}
	if cred.AccessToken != "login-access" {
		t.Errorf("AccessToken = %q", cred.AccessToken)
	}
	if len(progressMsgs) < 2 {
		t.Errorf("expected at least 2 progress messages, got %d", len(progressMsgs))
	}
}
