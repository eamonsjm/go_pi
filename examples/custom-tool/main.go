// Example: custom-tool demonstrates registering a custom tool with the SDK.
//
// This example adds a "weather" tool that returns mock weather data,
// showing how to extend the agent with domain-specific capabilities.
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-...
//	go run ./examples/custom-tool "What's the weather in San Francisco?"
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/sdk"
	"github.com/ejm/go_pi/pkg/tools"
)

// WeatherTool is a custom tool that returns mock weather data.
type WeatherTool struct{}

func (w *WeatherTool) Name() string        { return "get_weather" }
func (w *WeatherTool) Description() string { return "Get current weather for a city" }
func (w *WeatherTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{
				"type":        "string",
				"description": "City name (e.g. 'San Francisco')",
			},
		},
		"required": []string{"city"},
	}
}

func (w *WeatherTool) Execute(_ context.Context, params map[string]any) (string, error) {
	city, _ := params["city"].(string)
	if city == "" {
		return "", fmt.Errorf("city parameter is required")
	}
	// Mock weather data.
	return fmt.Sprintf("Weather in %s: 68°F (20°C), partly cloudy, wind 12 mph W", city), nil
}

func main() {
	prompt := strings.Join(os.Args[1:], " ")
	if prompt == "" {
		prompt = "What's the weather in San Francisco and New York?"
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable is required")
	}

	// Create a custom registry with only our weather tool (no file system access).
	registry := tools.NewRegistry()
	registry.Register(&WeatherTool{})

	ctx := context.Background()

	s, err := sdk.NewSession(ctx,
		sdk.WithAPIKey("anthropic", apiKey),
		sdk.WithTools(registry),
		sdk.WithSystemPrompt("You are a helpful weather assistant. Use the get_weather tool to answer questions about weather."),
	)
	if err != nil {
		log.Fatal(err)
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
			fmt.Fprintf(os.Stderr, "[calling tool: %s]\n", event.ToolName)
		case agent.EventToolExecEnd:
			fmt.Fprintf(os.Stderr, "[tool result: %s]\n", event.ToolResult)
		}
	}
	fmt.Println()

	if err := <-errCh; err != nil {
		log.Fatal(err)
	}
}
