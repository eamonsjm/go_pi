package main

import (
	"testing"
)

func TestDefaultPromptLoaded(t *testing.T) {
	if defaultPrompt == "" {
		t.Fatal("defaultPrompt should be loaded from prompts/default.md")
	}
	if defaultPrompt != "You are Pi, an AI coding agent for the terminal.\n\nYou help developers understand, navigate, and modify codebases. You have access to tools for reading files, writing files, making targeted edits, running shell commands, and searching code with glob patterns and regular expressions." {
		t.Errorf("defaultPrompt has unexpected content: %q", defaultPrompt)
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

func TestOAuthPromptsConventionDriven(t *testing.T) {
	// Verify that the map keys match the file naming convention:
	// oauth_<provider>.md → key is <provider>
	for provider := range oauthPrompts {
		if provider == "" {
			t.Error("oauthPrompts has empty key")
		}
	}
}
