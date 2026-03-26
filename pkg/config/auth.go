package config

// providerAPIKeyEnvVars maps provider names to their API key environment variable names.
// Only providers whose env var holds an actual authentication credential belong here.
var providerAPIKeyEnvVars = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"gemini":     "GEMINI_API_KEY",
	"azure":      "AZURE_OPENAI_API_KEY",
}

// providerConfigEnvVars maps provider names to non-credential configuration
// environment variables (e.g. region, host URL). These are required for the
// provider to function but are not API keys.
var providerConfigEnvVars = map[string]string{
	"bedrock": "AWS_DEFAULT_REGION",
	"ollama":  "OLLAMA_HOST",
}

// ProviderAPIKeyEnvVar returns the environment variable name for a provider's API key.
// Returns ("", false) if the provider has no API key env var.
func ProviderAPIKeyEnvVar(provider string) (string, bool) {
	v, ok := providerAPIKeyEnvVars[provider]
	return v, ok
}

// ProviderConfigEnvVar returns the non-credential configuration environment variable
// for a provider. Returns ("", false) if the provider has no config env var.
func ProviderConfigEnvVar(provider string) (string, bool) {
	v, ok := providerConfigEnvVars[provider]
	return v, ok
}

// isValidProvider reports whether name is a known provider.
func isValidProvider(name string) bool {
	_, apiOK := providerAPIKeyEnvVars[name]
	_, cfgOK := providerConfigEnvVars[name]
	return apiOK || cfgOK
}

// ValidProviderNames returns a sorted list of all known provider names.
func ValidProviderNames() []string {
	seen := make(map[string]struct{}, len(providerAPIKeyEnvVars)+len(providerConfigEnvVars))
	for k := range providerAPIKeyEnvVars {
		seen[k] = struct{}{}
	}
	for k := range providerConfigEnvVars {
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
