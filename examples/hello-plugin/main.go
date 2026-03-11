// hello-plugin is a minimal Pi plugin that demonstrates the JSONL protocol.
//
// It provides:
//   - A tool called "hello" that greets a person by name.
//   - A slash command called "greet" that prints a greeting.
//
// Build: go build -o hello-plugin .
// Usage: place in ~/.pi/plugins/hello-plugin/ alongside a plugin.json manifest.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// --- Protocol types (mirroring the host's types) ---

// HostMessage is a message received from the Pi host.
type HostMessage struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Args   string         `json:"args,omitempty"`
	Config map[string]any `json:"config,omitempty"`
	Event  map[string]any `json:"event,omitempty"`
}

// PluginResponse is a message sent back to the Pi host.
type PluginResponse struct {
	Type     string       `json:"type"`
	ID       string       `json:"id,omitempty"`
	Content  string       `json:"content,omitempty"`
	Text     string       `json:"text,omitempty"`
	IsError  bool         `json:"is_error,omitempty"`
	Tools    []ToolDef    `json:"tools,omitempty"`
	Commands []CommandDef `json:"commands,omitempty"`
	Role     string       `json:"role,omitempty"`
	Level    string       `json:"level,omitempty"`
	Message  string       `json:"message,omitempty"`
}

// ToolDef describes a tool this plugin provides.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// CommandDef describes a slash command this plugin provides.
type CommandDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg HostMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			sendLog("error", fmt.Sprintf("failed to parse message: %v", err))
			continue
		}

		switch msg.Type {
		case "initialize":
			handleInitialize()

		case "tool_call":
			handleToolCall(msg)

		case "command":
			handleCommand(msg)

		case "event":
			// Events are fire-and-forget; no response needed.
			// A real plugin might log, collect metrics, etc.

		case "shutdown":
			os.Exit(0)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "hello-plugin: scanner error: %v\n", err)
		os.Exit(1)
	}
}

func handleInitialize() {
	send(PluginResponse{
		Type: "capabilities",
		Tools: []ToolDef{
			{
				Name:        "hello",
				Description: "Greet a person by name. Returns a friendly hello message.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "The name of the person to greet",
						},
					},
					"required": []string{"name"},
				},
			},
		},
		Commands: []CommandDef{
			{
				Name:        "greet",
				Description: "Print a greeting message (usage: /greet <name>)",
			},
		},
	})

	sendLog("info", "hello-plugin initialized successfully")
}

func handleToolCall(msg HostMessage) {
	if msg.Name != "hello" {
		send(PluginResponse{
			Type:    "tool_result",
			ID:      msg.ID,
			Content: fmt.Sprintf("unknown tool: %s", msg.Name),
			IsError: true,
		})
		return
	}

	name, _ := msg.Params["name"].(string)
	if name == "" {
		name = "world"
	}

	send(PluginResponse{
		Type:    "tool_result",
		ID:      msg.ID,
		Content: fmt.Sprintf("Hello, %s! Welcome to Pi.", name),
	})
}

func handleCommand(msg HostMessage) {
	if msg.Name != "greet" {
		send(PluginResponse{
			Type:    "command_result",
			Text:    fmt.Sprintf("unknown command: %s", msg.Name),
			IsError: true,
		})
		return
	}

	name := msg.Args
	if name == "" {
		name = "friend"
	}

	send(PluginResponse{
		Type: "command_result",
		Text: fmt.Sprintf("Greetings, %s! The hello-plugin sends its regards.", name),
	})
}

// send writes a PluginResponse as a JSONL line to stdout.
func send(resp PluginResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hello-plugin: failed to marshal response: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stdout, string(data))
}

// sendLog sends a log message to the host.
func sendLog(level, message string) {
	send(PluginResponse{
		Type:    "log",
		Level:   level,
		Message: message,
	})
}
