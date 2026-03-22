// Example: multi-model demonstrates switching between different AI models
// and providers to compare outputs and behavior.
//
// This example shows how to:
// - Create sessions with different AI models
// - Switch between providers (Anthropic, OpenAI, etc.)
// - Compare responses from different models
// - Use model-specific configurations
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-...
//	export OPENAI_API_KEY=sk-...
//	go run ./examples/multi-model
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/sdk"
)

func runModelComparison(ctx context.Context, provider, model, prompt string) {
	fmt.Printf("\n=== %s (%s) ===\n\n", provider, model)

	// Get API key for the provider
	var apiKey string
	switch provider {
	case "anthropic":
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		apiKey = os.Getenv("OPENAI_API_KEY")
	default:
		log.Printf("Provider %s not configured\n", provider)
		return
	}

	if apiKey == "" {
		log.Printf("API key for %s not set\n", provider)
		return
	}

	s, err := sdk.NewSession(ctx,
		sdk.WithAPIKey(provider, apiKey),
		sdk.WithModel(model),
		sdk.WithSystemPrompt("You are a helpful assistant. Be concise and direct."),
	)
	if err != nil {
		log.Printf("Error creating session for %s: %v\n", provider, err)
		return
	}
	defer func() { _ = s.Close() }()

	events := s.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Prompt(ctx, prompt)
	}()

	for event := range events {
		switch event.Type {
		case agent.EventAssistantText:
			fmt.Print(event.Delta)
		case agent.EventToolExecStart:
			fmt.Fprintf(os.Stderr, "\n[tool: %s]\n", event.ToolName)
		case agent.EventAgentError:
			fmt.Fprintf(os.Stderr, "Error: %v\n", event.Error)
		}
	}
	fmt.Println()

	if err := <-errCh; err != nil {
		log.Printf("Error: %v\n", err)
	}
}

func main() {
	// Check for required API keys
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	openaiKey := os.Getenv("OPENAI_API_KEY")

	if anthropicKey == "" && openaiKey == "" {
		log.Fatal("At least one API key (ANTHROPIC_API_KEY or OPENAI_API_KEY) is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C. The goroutine exits via ctx.Done when the prompt
	// ends, and signal.Stop unregisters the channel.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()

	prompt := "What are the three most important principles of clean code?"

	fmt.Println("Comparing different AI models and providers on the same prompt:")
	fmt.Printf("Prompt: %q\n", prompt)

	// Compare Anthropic models
	if anthropicKey != "" {
		runModelComparison(ctx, "anthropic", "claude-sonnet-4-20250514", prompt)
		runModelComparison(ctx, "anthropic", "claude-opus-4-20250805", prompt)
	}

	// Compare OpenAI models
	if openaiKey != "" {
		runModelComparison(ctx, "openai", "gpt-4o", prompt)
		runModelComparison(ctx, "openai", "gpt-4-turbo", prompt)
	}

	fmt.Println()
	fmt.Println("--- Comparison complete ---")
}
