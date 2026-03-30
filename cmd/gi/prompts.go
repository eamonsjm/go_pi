package main

import (
	_ "embed"
	"strings"
)

//go:embed prompts/default.md
var defaultPrompt string

//go:embed prompts/oauth_anthropic.md
var oauthAnthropicPrompt string

//go:embed prompts/oauth_openai.md
var oauthOpenAIPrompt string

// oauthBasePrompts maps provider names to the system prompt their OAuth
// path expects. Providers not in this map use defaultPrompt.
var oauthBasePrompts = map[string]*string{
	"anthropic": &oauthAnthropicPrompt,
	"openai":    &oauthOpenAIPrompt,
}

// selectBasePrompt picks the base identity prompt based on provider and OAuth state.
func selectBasePrompt(providerName string, isOAuth bool) string {
	if isOAuth {
		if prompt, ok := oauthBasePrompts[providerName]; ok {
			return strings.TrimSpace(*prompt)
		}
	}
	return strings.TrimSpace(defaultPrompt)
}
