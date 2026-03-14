package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/ejm/go_pi/pkg/agent"
)

// RunJSONStream runs the agent in JSON event stream mode. It reads a prompt
// from args (or stdin if args is empty), runs the agent, and writes each event
// as a newline-delimited JSON object to stdout.
func RunJSONStream(agentLoop *agent.AgentLoop, prompt string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	if prompt == "" {
		prompt = readStdin()
		if prompt == "" {
			data, _ := json.Marshal(Event{Type: "error", Error: "no prompt provided"})
			fmt.Fprintf(os.Stdout, "%s\n", data)
			os.Exit(1)
		}
	}

	enc := json.NewEncoder(os.Stdout)
	events := agentLoop.Events()

	go func() {
		if err := agentLoop.Prompt(ctx, prompt); err != nil {
			// Error is emitted as an agent_error event via the events channel.
		}
	}()

	for event := range events {
		ev := EventFromAgent(event)
		enc.Encode(ev) //nolint: errcheck

		if event.Type == agent.EventAgentEnd || event.Type == agent.EventAgentError {
			return
		}
	}
}

func readStdin() string {
	info, _ := os.Stdin.Stat()
	if info.Mode()&os.ModeCharDevice != 0 {
		// stdin is a terminal, not piped — return empty.
		return ""
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var text string
	for scanner.Scan() {
		if text != "" {
			text += "\n"
		}
		text += scanner.Text()
	}
	return text
}
