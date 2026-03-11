package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAnthropicAuthURL   = "https://console.anthropic.com"
	defaultAnthropicClientID  = "pi-cli"
	defaultDevicePollInterval = 5 // seconds
)

// AnthropicOAuth implements OAuthProvider for Anthropic using
// OAuth 2.0 device authorization grant (RFC 8628) with PKCE (RFC 7636).
type AnthropicOAuth struct {
	AuthURL    string
	ClientID   string
	HTTPClient *http.Client
}

// NewAnthropicOAuth creates an Anthropic OAuth provider with default settings.
func NewAnthropicOAuth() *AnthropicOAuth {
	return &AnthropicOAuth{
		AuthURL:    defaultAnthropicAuthURL,
		ClientID:   defaultAnthropicClientID,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *AnthropicOAuth) ID() string   { return "anthropic" }
func (a *AnthropicOAuth) Name() string { return "Anthropic (Claude)" }

// DeviceCodeResponse holds the response from the device authorization endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// tokenResponse is the JSON response from the token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	APIKey       string `json:"api_key,omitempty"`
}

// tokenErrorResponse is the error response from the token endpoint during polling.
type tokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RequestDeviceCode initiates the device authorization flow with PKCE.
// Returns the device code response and PKCE challenge (caller needs the
// verifier for ExchangeDeviceCode).
func (a *AnthropicOAuth) RequestDeviceCode() (*DeviceCodeResponse, *PKCEChallenge, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, nil, fmt.Errorf("generate PKCE: %w", err)
	}

	form := url.Values{
		"client_id":             {a.ClientID},
		"scope":                 {"api"},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
	}

	resp, err := a.HTTPClient.PostForm(a.AuthURL+"/oauth/authorize/device", form)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("device code request failed (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result DeviceCodeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, pkce, nil
}

// ExchangeDeviceCode polls the token endpoint until the user completes
// authorization or the device code expires.
func (a *AnthropicOAuth) ExchangeDeviceCode(deviceCode, codeVerifier string, interval, timeout time.Duration) (*Credential, error) {
	if interval <= 0 {
		interval = time.Duration(defaultDevicePollInterval) * time.Second
	}
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("authorization timed out — device code expired")
		}

		time.Sleep(interval)

		form := url.Values{
			"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code":   {deviceCode},
			"client_id":     {a.ClientID},
			"code_verifier": {codeVerifier},
		}

		resp, err := a.HTTPClient.PostForm(a.AuthURL+"/oauth/token", form)
		if err != nil {
			return nil, fmt.Errorf("poll request: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var token tokenResponse
			if err := json.Unmarshal(body, &token); err != nil {
				return nil, fmt.Errorf("parse token response: %w", err)
			}
			return a.tokenToCredential(&token), nil
		}

		var errResp tokenErrorResponse
		if err := json.Unmarshal(body, &errResp); err != nil {
			return nil, fmt.Errorf("parse error response (%d): %s",
				resp.StatusCode, string(body))
		}

		switch errResp.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "expired_token":
			return nil, fmt.Errorf("device code expired — please retry /login")
		case "access_denied":
			return nil, fmt.Errorf("authorization denied by user")
		default:
			return nil, fmt.Errorf("token error: %s: %s",
				errResp.Error, errResp.ErrorDescription)
		}
	}
}

// Login implements OAuthProvider.Login by running the full device code + PKCE
// flow, using callbacks for user interaction.
func (a *AnthropicOAuth) Login(cb OAuthCallbacks) (*Credential, error) {
	deviceResp, pkce, err := a.RequestDeviceCode()
	if err != nil {
		return nil, err
	}

	if cb.OnAuth != nil {
		cb.OnAuth(deviceResp.VerificationURI,
			fmt.Sprintf("Enter the code: %s", deviceResp.UserCode))
	}
	if cb.OnProgress != nil {
		cb.OnProgress("Waiting for authorization...")
	}

	interval := time.Duration(deviceResp.Interval) * time.Second
	timeout := time.Duration(deviceResp.ExpiresIn) * time.Second

	cred, err := a.ExchangeDeviceCode(
		deviceResp.DeviceCode, pkce.Verifier, interval, timeout)
	if err != nil {
		return nil, err
	}

	if cb.OnProgress != nil {
		cb.OnProgress("Authorization complete!")
	}
	return cred, nil
}

// RefreshToken implements OAuthProvider.RefreshToken.
func (a *AnthropicOAuth) RefreshToken(cred *Credential) (*Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
		"client_id":     {a.ClientID},
	}

	resp, err := a.HTTPClient.PostForm(a.AuthURL+"/oauth/token", form)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var errResp tokenErrorResponse
		json.Unmarshal(body, &errResp)
		return nil, fmt.Errorf("refresh failed (%d): %s",
			resp.StatusCode, errResp.ErrorDescription)
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	refreshed := a.tokenToCredential(&token)
	// Keep old refresh token if server didn't issue a new one.
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = cred.RefreshToken
	}
	return refreshed, nil
}

// GetAPIKey implements OAuthProvider.GetAPIKey.
func (a *AnthropicOAuth) GetAPIKey(cred *Credential) string {
	if cred.Key != "" {
		return cred.Key
	}
	return cred.AccessToken
}

// tokenToCredential converts a token response to a Credential.
func (a *AnthropicOAuth) tokenToCredential(token *tokenResponse) *Credential {
	cred := &Credential{
		Type:         CredentialOAuth,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UnixMilli(),
	}
	if token.APIKey != "" {
		cred.Key = token.APIKey
	}
	return cred
}
