package config

// ProviderAPIKeyEnvVars maps provider names to their API key environment variable names.
// Only providers whose env var holds an actual authentication credential belong here.
var ProviderAPIKeyEnvVars = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"gemini":     "GEMINI_API_KEY",
	"azure":      "AZURE_OPENAI_API_KEY",
}

// ProviderConfigEnvVars maps provider names to non-credential configuration
// environment variables (e.g. region, host URL). These are required for the
// provider to function but are not API keys.
var ProviderConfigEnvVars = map[string]string{
	"bedrock": "AWS_DEFAULT_REGION",
	"ollama":  "OLLAMA_HOST",
}

// ValidProviderNames returns a sorted list of all known provider names.
func ValidProviderNames() []string {
	seen := make(map[string]struct{}, len(ProviderAPIKeyEnvVars)+len(ProviderConfigEnvVars))
	for k := range ProviderAPIKeyEnvVars {
		seen[k] = struct{}{}
	}
	for k := range ProviderConfigEnvVars {
		seen[k] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	// sort inline to avoid an import; callers already import sort
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}
