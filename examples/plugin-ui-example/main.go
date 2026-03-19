// Example plugin demonstrating the UI API.
// This plugin shows how to request various UI components from the host.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
)

type HostMessage struct {
	Type       string         `json:"type"`
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	UIResponse map[string]any `json:"ui_response,omitempty"`
}

type PluginMessage struct {
	Type          string       `json:"type"`
	ID            string       `json:"id,omitempty"`
	Content       string       `json:"content,omitempty"`
	IsError       bool         `json:"is_error,omitempty"`
	Tools         []ToolDef    `json:"tools,omitempty"`
	Commands      []CommandDef `json:"commands,omitempty"`
	UIType        string       `json:"ui_type,omitempty"`
	UITitle       string       `json:"ui_title,omitempty"`
	UIOptions     []string     `json:"ui_options,omitempty"`
	UIDefault     string       `json:"ui_default,omitempty"`
	UINotifyLevel string       `json:"ui_notify_level,omitempty"`
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

var pendingUIRequests = make(map[string]map[string]any)

func send(msg PluginMessage) {
	data, _ := json.Marshal(msg)
	fmt.Fprintln(os.Stdout, string(data))
}

func requestUI(uiType, title string, options []string, defaultVal string) (string, error) {
	id := randomID()
	pendingUIRequests[id] = map[string]any{
		"type":     uiType,
		"title":    title,
		"options":  options,
		"default":  defaultVal,
		"resolved": false,
	}

	send(PluginMessage{
		Type:      "ui_request",
		ID:        id,
		UIType:    uiType,
		UITitle:   title,
		UIOptions: options,
		UIDefault: defaultVal,
	})

	// In a real plugin, you'd wait for the response asynchronously
	// For this example, we'll return a placeholder
	return "", nil
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("req_%x", b)
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Send capabilities
	send(PluginMessage{
		Type: "capabilities",
		Tools: []ToolDef{
			{
				Name:        "ask_name",
				Description: "Ask the user for their name via input dialog",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"greeting": map[string]any{
							"type":        "string",
							"description": "Custom greeting message",
						},
					},
				},
			},
			{
				Name:        "choose_color",
				Description: "Ask the user to choose a color",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
			{
				Name:        "confirm_action",
				Description: "Ask the user to confirm an action",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"description": "The action to confirm",
						},
					},
				},
			},
			{
				Name:        "get_bio",
				Description: "Ask the user to enter a bio via editor dialog",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
			{
				Name:        "send_notification",
				Description: "Send a notification to the user",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{
							"type":        "string",
							"description": "Notification message",
						},
						"level": map[string]any{
							"type":        "string",
							"description": "Level: info, warning, error",
							"enum":        []string{"info", "warning", "error"},
						},
					},
					"required": []string{"message", "level"},
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
			handleToolCall(msg)
		case "ui_response":
			// Plugin receives response to UI request
			if response, ok := msg.UIResponse["id"]; ok {
				id := response.(string)
				if req, exists := pendingUIRequests[id]; exists {
					req["resolved"] = true
					req["response"] = msg.UIResponse
				}
			}
		case "shutdown":
			os.Exit(0)
		}
	}
}

func handleToolCall(msg HostMessage) {
	toolName := msg.Name
	toolID := msg.ID

	switch toolName {
	case "ask_name":
		greeting := "What is your name?"
		if params, ok := msg.Params["greeting"].(string); ok && params != "" {
			greeting = params
		}
		send(PluginMessage{
			Type:      "ui_request",
			ID:        toolID,
			UIType:    "input",
			UITitle:   greeting,
			UIDefault: "John Doe",
		})
		// Tool will complete when ui_response is received
		// For now, just send the request

	case "choose_color":
		send(PluginMessage{
			Type:      "ui_request",
			ID:        toolID,
			UIType:    "select",
			UITitle:   "Choose your favorite color:",
			UIOptions: []string{"red", "green", "blue", "yellow", "purple"},
		})

	case "confirm_action":
		action := "Do you want to continue?"
		if params, ok := msg.Params["action"].(string); ok && params != "" {
			action = fmt.Sprintf("Do you want to %s?", params)
		}
		send(PluginMessage{
			Type:    "ui_request",
			ID:      toolID,
			UIType:  "confirm",
			UITitle: action,
		})

	case "get_bio":
		send(PluginMessage{
			Type:      "ui_request",
			ID:        toolID,
			UIType:    "editor",
			UITitle:   "Tell us about yourself:",
			UIDefault: "Enter your bio here...",
		})

	case "send_notification":
		message := ""
		level := "info"
		if params, ok := msg.Params["message"].(string); ok {
			message = params
		}
		if params, ok := msg.Params["level"].(string); ok {
			level = params
		}

		send(PluginMessage{
			Type:          "ui_request",
			ID:            toolID,
			UIType:        "notify",
			UITitle:       message,
			UINotifyLevel: level,
		})

		// For notifications, immediately return success since they don't need user response
		send(PluginMessage{
			Type:    "tool_result",
			ID:      toolID,
			Content: fmt.Sprintf("Notification sent: %s", message),
		})

	default:
		send(PluginMessage{
			Type:    "tool_result",
			ID:      toolID,
			Content: fmt.Sprintf("Unknown tool: %s", toolName),
			IsError: true,
		})
	}
}
