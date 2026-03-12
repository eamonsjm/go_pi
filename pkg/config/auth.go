package config

// ProviderEnvVars maps provider names to their API key environment variable names.
var ProviderEnvVars = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"gemini":     "GEMINI_API_KEY",
	"azure":      "AZURE_OPENAI_API_KEY",
}
