// Package tui implements the terminal user interface for the coding agent.
//
// # Component construction
//
// UI components ([App], [ChatView], [Editor], [Header], [Footer],
// [ModelSelector], [CommandRegistry], [KeybindingConfig]) require explicit
// construction via their New*/Load* functions. Their zero values contain nil
// maps, nil renderers, or zero dimensions and will panic or produce no output
// if used directly. This is standard for stateful Bubble Tea components.
//
// Message types ([StreamEventMsg], [AgentDoneMsg], [CommandResultMsg], etc.)
// are plain value types and have useful zero values.
package tui
