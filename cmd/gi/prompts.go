package main

import _ "embed"

//go:embed prompts/default.md
var defaultPrompt string

//go:embed prompts/oauth_anthropic.md
var oauthAnthropicPrompt string

//go:embed prompts/oauth_openai.md
var oauthOpenAIPrompt string
