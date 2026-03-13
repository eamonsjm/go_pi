// Example Gi plugin using the Go SDK.
//
// This plugin provides a "word_count" tool and a "wc" command.
// Build: go build -o word-counter .
// Install: cp word-counter ~/.gi/plugins/word-counter/
package main

import (
	"fmt"
	"strings"

	"github.com/ejm/go_pi/pkg/plugin/sdk"
)

func main() {
	p := sdk.NewPlugin("word-counter").
		Tool("word_count", "Count words in text", sdk.Schema(
			sdk.Prop("text", "string", "The text to count words in"),
			sdk.Required("text"),
		), func(ctx sdk.ToolContext) (string, error) {
			text, _ := ctx.Params["text"].(string)
			words := strings.Fields(text)
			return fmt.Sprintf("%d words", len(words)), nil
		}).
		Command("wc", "Count words in the given text", func(ctx sdk.CommandContext) (string, error) {
			words := strings.Fields(ctx.Args)
			return fmt.Sprintf("%d words", len(words)), nil
		}).
		OnEvent(func(e sdk.Event) {
			// Observe agent events (fire-and-forget, no response needed).
		})

	p.Run()
}
