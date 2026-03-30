package main

import (
	"testing"
)

func TestDefaultPromptLoaded(t *testing.T) {
	if defaultBasePrompt == "" {
		t.Fatal("defaultBasePrompt should be loaded from prompts/default.md")
	}
	if defaultBasePrompt != "You are Pi, an AI coding agent for the terminal.\n\nYou help developers understand, navigate, and modify codebases. You have access to tools for reading files, writing files, making targeted edits, running shell commands, and searching code with glob patterns and regular expressions." {
		t.Errorf("defaultBasePrompt has unexpected content: %q", defaultBasePrompt)
	}
}

func TestOAuthPromptsPopulated(t *testing.T) {
	if len(oauthPrompts) == 0 {
		t.Fatal("oauthPrompts should be populated from oauth_*.md files")
	}

	for _, provider := range []string{"anthropic", "openai"} {
		prompt, ok := oauthPrompts[provider]
		if !ok {
			t.Errorf("oauthPrompts missing provider %q", provider)
			continue
		}
		if prompt == "" {
			t.Errorf("oauthPrompts[%q] is empty", provider)
		}
	}
}

func TestSelectBasePrompt(t *testing.T) {
	// Non-OAuth always returns default
	if got := selectBasePrompt("anthropic", false); got != defaultBasePrompt {
		t.Errorf("non-OAuth should return default, got %q", got)
	}

	// OAuth with known provider returns provider-specific prompt
	for provider, want := range oauthPrompts {
		if got := selectBasePrompt(provider, true); got != want {
			t.Errorf("selectBasePrompt(%q, true) = %q, want %q", provider, got, want)
		}
	}

	// OAuth with unknown provider falls back to default
	if got := selectBasePrompt("unknown-provider", true); got != defaultBasePrompt {
		t.Errorf("OAuth unknown provider should fall back to default, got %q", got)
	}
}

func TestOAuthPromptsConventionDriven(t *testing.T) {
	// Verify that the map keys match the file naming convention:
	// oauth_<provider>.md → key is <provider>
	for provider := range oauthPrompts {
		if provider == "" {
			t.Error("oauthPrompts has empty key")
		}
	}
}
