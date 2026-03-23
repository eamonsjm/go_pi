package config

import (
	"testing"
)

func TestProviderAPIKeyEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		wantVar  string
		wantOK   bool
	}{
		{"anthropic", "ANTHROPIC_API_KEY", true},
		{"openrouter", "OPENROUTER_API_KEY", true},
		{"openai", "OPENAI_API_KEY", true},
		{"gemini", "GEMINI_API_KEY", true},
		{"azure", "AZURE_OPENAI_API_KEY", true},
		// Providers with config vars but no API key
		{"bedrock", "", false},
		{"ollama", "", false},
		// Unknown provider
		{"nonexistent", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got, ok := ProviderAPIKeyEnvVar(tt.provider)
			if ok != tt.wantOK {
				t.Errorf("ProviderAPIKeyEnvVar(%q) ok = %v, want %v", tt.provider, ok, tt.wantOK)
			}
			if got != tt.wantVar {
				t.Errorf("ProviderAPIKeyEnvVar(%q) = %q, want %q", tt.provider, got, tt.wantVar)
			}
		})
	}
}

func TestProviderConfigEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		wantVar  string
		wantOK   bool
	}{
		{"bedrock", "AWS_DEFAULT_REGION", true},
		{"ollama", "OLLAMA_HOST", true},
		// Providers with API keys but no config var
		{"anthropic", "", false},
		{"openai", "", false},
		// Unknown provider
		{"nonexistent", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got, ok := ProviderConfigEnvVar(tt.provider)
			if ok != tt.wantOK {
				t.Errorf("ProviderConfigEnvVar(%q) ok = %v, want %v", tt.provider, ok, tt.wantOK)
			}
			if got != tt.wantVar {
				t.Errorf("ProviderConfigEnvVar(%q) = %q, want %q", tt.provider, got, tt.wantVar)
			}
		})
	}
}

func TestValidProviderNames(t *testing.T) {
	names := ValidProviderNames()

	// Must include all providers from both maps
	expected := []string{"anthropic", "azure", "bedrock", "gemini", "ollama", "openai", "openrouter"}

	if len(names) != len(expected) {
		t.Fatalf("ValidProviderNames() returned %d names, want %d: got %v", len(names), len(expected), names)
	}

	// Verify sorted order and exact contents
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("ValidProviderNames()[%d] = %q, want %q", i, names[i], want)
		}
	}
}

func TestValidProviderNames_sorted(t *testing.T) {
	names := ValidProviderNames()
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("ValidProviderNames() not sorted: %q before %q at index %d", names[i-1], names[i], i)
		}
	}
}
