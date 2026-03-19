# Plugin UI API Documentation

This document describes how plugins can request interactive UI components from the host application.

## Overview

Plugins can request UI interactions with the host through the **UI Request/Response** protocol. The host can display dialogs, notifications, and status updates, and return user input back to the plugin.

## UI Request Protocol

### From Plugin to Host: UI Request

Plugins send a `ui_request` message with the following structure:

```json
{
  "type": "ui_request",
  "id": "plugin_req_abc123",
  "ui_type": "input",
  "ui_title": "Enter your name:",
  "ui_default": "John Doe"
}
```

### From Host to Plugin: UI Response

The host responds with a `ui_response` message:

```json
{
  "type": "ui_response",
  "ui_response": {
    "id": "plugin_req_abc123",
    "value": "Jane Smith",
    "closed": false,
    "error": ""
  }
}
```

## UI Types

### 1. Input Dialog (`input`)

Prompts the user for text input.

**Request:**
```json
{
  "type": "ui_request",
  "id": "req_1",
  "ui_type": "input",
  "ui_title": "Enter your email:",
  "ui_default": "user@example.com"
}
```

**Response:**
- `value`: The text entered by the user
- `closed`: `true` if the user cancelled without responding
- `error`: Error message if the dialog failed to open

### 2. Confirm Dialog (`confirm`)

Asks the user to confirm an action with yes/no.

**Request:**
```json
{
  "type": "ui_request",
  "id": "req_2",
  "ui_type": "confirm",
  "ui_title": "Delete file?"
}
```

**Response:**
- `value`: `"yes"` or `"no"` (or empty if closed)
- `closed`: `true` if the user closed the dialog

### 3. Select Dialog (`select`)

Presents the user with a list of options to choose from.

**Request:**
```json
{
  "type": "ui_request",
  "id": "req_3",
  "ui_type": "select",
  "ui_title": "Choose a theme:",
  "ui_options": ["light", "dark", "auto"]
}
```

**Response:**
- `value`: The selected option from the list
- `closed`: `true` if the user cancelled

### 4. Editor Dialog (`editor`)

Opens a text editor for longer text input.

**Request:**
```json
{
  "type": "ui_request",
  "id": "req_4",
  "ui_type": "editor",
  "ui_title": "Edit your bio:",
  "ui_default": "Tell us about yourself..."
}
```

**Response:**
- `value`: The edited text
- `closed`: `true` if the user cancelled

### 5. Notification (`notify`)

Sends a non-blocking notification to the user. This doesn't expect a response.

**Request:**
```json
{
  "type": "ui_request",
  "id": "req_5",
  "ui_type": "notify",
  "ui_title": "Processing complete",
  "ui_notify_level": "info"
}
```

The response is still sent (with empty `value`), allowing the plugin to confirm delivery.

## Headless Mode Degradation

When running in headless mode (RPC, JSON stream, or print mode), UI requests should return sensible defaults:

- **input**: Return `ui_default` value, or empty string if not provided
- **confirm**: Return `"no"` (conservative default)
- **select**: Return the first option, or empty string
- **editor**: Return `ui_default` value, or empty string
- **notify**: Log the notification and return success

### Configuration

Plugins can check if the host supports UI by checking for a `hasUI` capability. Plugins should gracefully degrade when UI is not available.

## Implementation in Plugins

### Go Example

```go
type UIRequest struct {
	ID            string   `json:"id"`
	Type          string   `json:"ui_type"`
	Title         string   `json:"ui_title,omitempty"`
	Options       []string `json:"ui_options,omitempty"`
	DefaultValue  string   `json:"ui_default,omitempty"`
	NotifyLevel   string   `json:"ui_notify_level,omitempty"`
}

// Send a UI request
func requestInput(title, defaultVal string) (string, error) {
	id := "req_" + randomID()
	msg := map[string]interface{}{
		"type":        "ui_request",
		"id":          id,
		"ui_type":    "input",
		"ui_title":   title,
		"ui_default": defaultVal,
	}
	sendJSON(msg)

	// Wait for response
	response := waitForResponse(id)
	if response["closed"].(bool) {
		return "", errors.New("user cancelled")
	}
	return response["value"].(string), nil
}
```

### JavaScript Example

```javascript
async function requestInput(title, defaultValue) {
    const id = "req_" + Math.random().toString(16).substring(2);

    const message = {
        type: "ui_request",
        id: id,
        ui_type: "input",
        ui_title: title,
        ui_default: defaultValue
    };

    sendJSON(message);

    // Wait for response
    const response = await waitForResponse(id);
    if (response.closed) {
        throw new Error("User cancelled");
    }
    return response.value;
}
```

## Error Handling

The `error` field in the UI response contains error messages if the dialog failed to open:

- `"ui_not_available"`: Host doesn't support UI requests
- `"dialog_timeout"`: User took too long to respond
- `"dialog_error"`: Internal error rendering the dialog

Plugins should handle these errors gracefully.

## Best Practices

1. **Always provide defaults**: Use `ui_default` to provide sensible defaults for input/editor
2. **Handle cancellation**: Check the `closed` field and provide fallback behavior
3. **Degrade gracefully**: Have fallback behavior for when UI is not available
4. **Use meaningful titles**: Make dialog titles clear and user-friendly
5. **Timeouts**: Be prepared for responses that take a while (user may be busy)
6. **IDs**: Use unique IDs for each request to correlate responses
7. **Non-blocking notifications**: Use `notify` for status updates instead of interrupting with dialogs

## Integration Notes for Host Implementers

### TUI (Bubble Tea)

The TUI should:
1. Poll plugin processes for `ui_request` messages via `WaitUIRequest()`
2. Render dialogs using Bubble Tea components
3. Capture user input and send `ui_response` via `RespondToUIRequest()`
4. Handle timeouts and cancellations

### Headless Modes

For RPC/JSON/print modes:
1. Return sensible defaults without user interaction
2. Log notifications to stderr
3. Treat cancellation as user not responding (timeout)

### Example Flow

```
1. Plugin calls tool_call
2. Plugin sends ui_request message
3. Host receives ui_request via WaitUIRequest()
4. Host renders dialog
5. User provides input
6. Host sends ui_response
7. Plugin receives ui_response
8. Plugin completes tool_call with tool_result
```
