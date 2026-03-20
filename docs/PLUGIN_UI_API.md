# Plugin UI API

Plugins can request interactive UI components from the host application through the plugin UI API.

## Overview

The plugin UI API enables plugins to:
- Request user input via dialogs (text input, selection, confirmation, editor)
- Send non-blocking notifications
- Handle responses asynchronously

## Message Protocol

### UI Request (Plugin → Host)

Plugins send `ui_request` messages to request UI interactions:

```json
{
  "type": "ui_request",
  "id": "unique-request-id",
  "ui_type": "input|select|confirm|editor|notify",
  "ui_title": "Display title or prompt",
  "ui_options": ["option1", "option2"],
  "ui_default": "default value",
  "ui_notify_level": "info|warning|error"
}
```

### UI Response (Host → Plugin)

The host responds with a `ui_response` message:

```json
{
  "type": "ui_response",
  "id": "unique-request-id",
  "value": "user's response or selection",
  "closed": false,
  "error": ""
}
```

## Dialog Types

### Input Dialog (`input`)

Prompts the user to enter text.

**Request Fields:**
- `ui_title`: Prompt text
- `ui_default`: Initial/default value

**Response:**
- `value`: Text entered by the user
- `closed`: true if user closed without responding

**Example:**
```go
send(PluginMessage{
    Type:      "ui_request",
    ID:        "ask_name",
    UIType:    "input",
    UITitle:   "What is your name?",
    UIDefault: "John Doe",
})
```

### Select Dialog (`select`)

Prompts the user to choose from a list of options.

**Request Fields:**
- `ui_title`: Prompt text
- `ui_options`: Array of choices

**Response:**
- `value`: The selected option
- `closed`: true if user closed without selecting

**Example:**
```go
send(PluginMessage{
    Type:      "ui_request",
    ID:        "choose_color",
    UIType:    "select",
    UITitle:   "Choose your favorite color:",
    UIOptions: []string{"red", "green", "blue"},
})
```

### Confirm Dialog (`confirm`)

Prompts the user to confirm (yes/no).

**Request Fields:**
- `ui_title`: Question text

**Response:**
- `value`: "true" for yes, "false" for no
- `closed`: true if user closed without responding

**Example:**
```go
send(PluginMessage{
    Type:    "ui_request",
    ID:      "confirm_action",
    UIType:  "confirm",
    UITitle: "Do you want to continue?",
})
```

### Editor Dialog (`editor`)

Opens a text editor for multi-line input.

**Request Fields:**
- `ui_title`: Prompt text
- `ui_default`: Initial content

**Response:**
- `value`: Edited text
- `closed`: true if user closed without saving

**Example:**
```go
send(PluginMessage{
    Type:      "ui_request",
    ID:        "get_bio",
    UIType:    "editor",
    UITitle:   "Tell us about yourself:",
    UIDefault: "Enter your bio here...",
})
```

### Notification (`notify`)

Sends a non-blocking notification (fire-and-forget).

**Request Fields:**
- `ui_title`: Notification text
- `ui_notify_level`: "info", "warning", or "error"

**Response:**
- The host does NOT send a response for notifications

**Example:**
```go
send(PluginMessage{
    Type:          "ui_request",
    ID:            "notify_success",
    UIType:        "notify",
    UITitle:       "Operation completed successfully!",
    UINotifyLevel: "info",
})
```

## Handling Responses

Plugins should maintain a mapping of request IDs to pending operations:

```go
var pendingRequests = make(map[string]PendingOperation)

// When sending a request
id := randomID()
pendingRequests[id] = operation

// In the message loop, handle ui_response
case "ui_response":
    if op, ok := pendingRequests[msg.ID]; ok {
        // Process the response
        handleResponse(op, msg.Value, msg.Closed)
        delete(pendingRequests, msg.ID)
    }
```

## Graceful Degradation (Headless Mode)

When the host application runs in headless mode (no TUI, print mode, RPC mode), plugins still receive `ui_response` messages with sensible defaults:

- **input/editor**: Returns the `ui_default` value
- **select**: Returns the first option from `ui_options`
- **confirm**: Returns "false"
- **notify**: Fire-and-forget (no response)

All responses in headless mode have `closed: true` and `error: "UI not available in headless mode"`.

Plugins should handle these defaults gracefully or check for error conditions.

## Example Plugin

See `examples/plugin-ui-example/main.go` for a complete example implementing:
- Input dialog (`ask_name` tool)
- Selection dialog (`choose_color` tool)
- Confirmation dialog (`confirm_action` tool)
- Editor dialog (`get_bio` tool)
- Notifications (`send_notification` tool)

## Host Implementation Notes

The host application must:

1. **Listen for UI requests**: Monitor the `UIRequests()` channel from each plugin process
2. **Render dialogs**: Display appropriate UI components for each dialog type
3. **Capture user input**: Collect responses from user interactions
4. **Send responses**: Call `RespondToUIRequest()` with the response
5. **Handle headless mode**: Provide sensible defaults when no TUI is available

Plugins are designed to work transparently across interactive and headless modes.
