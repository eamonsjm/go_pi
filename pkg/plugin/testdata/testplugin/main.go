// testplugin is a test plugin binary for integration testing of the plugin system.
// It implements the full JSONL protocol and supports multiple modes controlled by
// the PLUGIN_MODE environment variable.
//
// Modes:
//   - normal:          Full-featured plugin with tools, commands, events, inject_message
//   - crash_on_tool:   Initializes normally, exits non-zero on first tool_call
//   - slow_init:       Sleeps 30s before responding to initialize (timeout test)
//   - inject_on_init:  Sends inject_message and log immediately after capabilities
//   - event_recorder:  Records received events and returns them via tool_call
//   - ui_request:      Sends UI request messages (dialog, notify) and handles responses
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type HostMessage struct {
	Type       string         `json:"type"`
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	Args       string         `json:"args,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
	Event      map[string]any `json:"event,omitempty"`
	UIResponse map[string]any `json:"ui_response,omitempty"`
}

type PluginResponse struct {
	Type           string       `json:"type"`
	ID             string       `json:"id,omitempty"`
	Content        string       `json:"content,omitempty"`
	Text           string       `json:"text,omitempty"`
	IsError        bool         `json:"is_error,omitempty"`
	Tools          []ToolDef    `json:"tools,omitempty"`
	Commands       []CommandDef `json:"commands,omitempty"`
	Role           string       `json:"role,omitempty"`
	Level          string       `json:"level,omitempty"`
	Message        string       `json:"message,omitempty"`
	UIType         string       `json:"ui_type,omitempty"`
	UITitle        string       `json:"ui_title,omitempty"`
	UIOptions      []string     `json:"ui_options,omitempty"`
	UIDefault      string       `json:"ui_default,omitempty"`
	UIValue        string       `json:"ui_value,omitempty"`
	UINotifyLevel  string       `json:"ui_notify_level,omitempty"`
}

type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type CommandDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func send(resp PluginResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testplugin: marshal error: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stdout, string(data))
}

func main() {
	mode := os.Getenv("PLUGIN_MODE")
	if mode == "" {
		mode = "normal"
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	switch mode {
	case "normal":
		runNormal(scanner)
	case "crash_on_tool":
		runCrashOnTool(scanner)
	case "slow_init":
		runSlowInit(scanner)
	case "inject_on_init":
		runInjectOnInit(scanner)
	case "event_recorder":
		runEventRecorder(scanner)
	case "ui_request":
		runUIRequest(scanner)
	default:
		fmt.Fprintf(os.Stderr, "testplugin: unknown mode: %s\n", mode)
		os.Exit(2)
	}
}

func runNormal(scanner *bufio.Scanner) {
	// Wait for initialize.
	if !scanner.Scan() {
		os.Exit(1)
	}

	send(PluginResponse{
		Type: "capabilities",
		Tools: []ToolDef{
			{
				Name:        "reverse",
				Description: "Reverses the input text",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "Text to reverse",
						},
					},
					"required": []string{"text"},
				},
			},
			{
				Name:        "upper",
				Description: "Uppercases the input text",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "Text to uppercase",
						},
					},
					"required": []string{"text"},
				},
			},
		},
		Commands: []CommandDef{
			{Name: "test-cmd", Description: "A test command"},
		},
	})

	for scanner.Scan() {
		var msg HostMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "tool_call":
			handleToolCall(msg)
		case "command":
			handleCommand(msg)
		case "event":
			// fire-and-forget
		case "shutdown":
			os.Exit(0)
		}
	}
}

func handleToolCall(msg HostMessage) {
	text, _ := msg.Params["text"].(string)
	switch msg.Name {
	case "reverse":
		runes := []rune(text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		send(PluginResponse{
			Type:    "tool_result",
			ID:      msg.ID,
			Content: string(runes),
		})
	case "upper":
		send(PluginResponse{
			Type:    "tool_result",
			ID:      msg.ID,
			Content: strings.ToUpper(text),
		})
	default:
		send(PluginResponse{
			Type:    "tool_result",
			ID:      msg.ID,
			Content: fmt.Sprintf("unknown tool: %s", msg.Name),
			IsError: true,
		})
	}
}

func handleCommand(msg HostMessage) {
	switch msg.Name {
	case "test-cmd":
		send(PluginResponse{
			Type: "command_result",
			Text: fmt.Sprintf("test-cmd executed with args: %s", msg.Args),
		})
	default:
		send(PluginResponse{
			Type:    "command_result",
			Text:    fmt.Sprintf("unknown command: %s", msg.Name),
			IsError: true,
		})
	}
}

func runCrashOnTool(scanner *bufio.Scanner) {
	if !scanner.Scan() {
		os.Exit(1)
	}

	send(PluginResponse{
		Type: "capabilities",
		Tools: []ToolDef{
			{Name: "crash", Description: "Crashes on call"},
		},
	})

	for scanner.Scan() {
		var msg HostMessage
		json.Unmarshal(scanner.Bytes(), &msg)
		if msg.Type == "tool_call" {
			os.Exit(1)
		}
		if msg.Type == "shutdown" {
			os.Exit(0)
		}
	}
}

func runSlowInit(scanner *bufio.Scanner) {
	// Read initialize message but sleep instead of responding.
	scanner.Scan()
	time.Sleep(30 * time.Second)
}

func runInjectOnInit(scanner *bufio.Scanner) {
	if !scanner.Scan() {
		os.Exit(1)
	}

	send(PluginResponse{
		Type: "capabilities",
		Tools: []ToolDef{
			{Name: "noop", Description: "Does nothing"},
		},
	})

	// Send inject_message and log immediately.
	send(PluginResponse{
		Type:    "inject_message",
		Role:    "assistant",
		Content: "injected from testplugin",
	})
	send(PluginResponse{
		Type:    "log",
		Level:   "info",
		Message: "testplugin initialized",
	})

	for scanner.Scan() {
		var msg HostMessage
		json.Unmarshal(scanner.Bytes(), &msg)
		if msg.Type == "shutdown" {
			os.Exit(0)
		}
	}
}

func runEventRecorder(scanner *bufio.Scanner) {
	if !scanner.Scan() {
		os.Exit(1)
	}

	send(PluginResponse{
		Type: "capabilities",
		Tools: []ToolDef{
			{Name: "get_events", Description: "Returns recorded events"},
		},
	})

	var events []map[string]any

	for scanner.Scan() {
		var msg HostMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "event":
			events = append(events, msg.Event)
		case "tool_call":
			if msg.Name == "get_events" {
				data, _ := json.Marshal(events)
				send(PluginResponse{
					Type:    "tool_result",
					ID:      msg.ID,
					Content: string(data),
				})
			}
		case "shutdown":
			os.Exit(0)
		}
	}
}

func runUIRequest(scanner *bufio.Scanner) {
	if !scanner.Scan() {
		os.Exit(1)
	}

	send(PluginResponse{
		Type: "capabilities",
		Tools: []ToolDef{
			{
				Name:        "ask_name",
				Description: "Asks user for their name via UI",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
			{
				Name:        "confirm_action",
				Description: "Confirms an action via UI",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	})

	for scanner.Scan() {
		var msg HostMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "tool_call":
			handleUIToolCall(msg)
		case "ui_response":
			// Plugin receives the user's response to a UI request
			// In this test, we just acknowledge it
			if msg.UIResponse != nil {
				send(PluginResponse{
					Type: "inject_message",
					Role: "assistant",
					Content: fmt.Sprintf("UI response received: id=%s, value=%s, closed=%v",
						msg.UIResponse["id"], msg.UIResponse["value"], msg.UIResponse["closed"]),
				})
			}
		case "shutdown":
			os.Exit(0)
		}
	}
}

func handleUIToolCall(msg HostMessage) {
	switch msg.Name {
	case "ask_name":
		// Send a UI request (input dialog) to the host
		send(PluginResponse{
			Type:      "ui_request",
			ID:        msg.ID,
			UIType:    "input",
			UITitle:   "What is your name?",
			UIDefault: "John Doe",
		})
	case "confirm_action":
		// Send a UI request (confirm dialog) to the host
		send(PluginResponse{
			Type:    "ui_request",
			ID:      msg.ID,
			UIType:  "confirm",
			UITitle: "Do you want to continue?",
		})
	default:
		send(PluginResponse{
			Type:    "tool_result",
			ID:      msg.ID,
			Content: fmt.Sprintf("unknown tool: %s", msg.Name),
			IsError: true,
		})
	}
}
