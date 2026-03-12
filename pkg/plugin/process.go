package plugin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

const (
	// initTimeout is how long to wait for a plugin to respond to initialize.
	initTimeout = 10 * time.Second

	// toolTimeout is the default timeout for a tool_call or command execution.
	toolTimeout = 30 * time.Second

	// shutdownTimeout is how long to wait for a plugin to exit after shutdown.
	shutdownTimeout = 5 * time.Second

	// maxScannerBuffer is the maximum size of a single JSONL message (1 MB).
	maxScannerBuffer = 1024 * 1024
)

// PluginProcess manages a single plugin subprocess and its JSONL communication.
type PluginProcess struct {
	name         string
	path         string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	scanner      *bufio.Scanner
	tools        []ToolDef
	commands     []CommandDef
	capabilities []string

	mu     sync.Mutex
	closed bool

	// injectCh receives inject_message and log messages from the background reader.
	injectCh chan PluginMessage

	// responseCh receives tool_result, command_result, and capabilities messages.
	responseCh chan PluginMessage
}

// startPlugin spawns a plugin subprocess and sets up JSONL communication pipes.
func startPlugin(name, path string) (*PluginProcess, error) {
	cmd := exec.Command(path)
	cmd.Dir = ""

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	// stderr is intentionally not captured -- it goes to the host's stderr
	// for debugging purposes.

	if err := cmd.Start(); err != nil {
		stdoutPipe.Close()
		stdinPipe.Close()
		return nil, fmt.Errorf("starting plugin %s: %w", name, err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	p := &PluginProcess{
		name:       name,
		path:       path,
		cmd:        cmd,
		stdin:      stdinPipe,
		scanner:    scanner,
		injectCh:   make(chan PluginMessage, 64),
		responseCh: make(chan PluginMessage, 16),
	}

	// Start background reader that routes incoming messages.
	go p.readLoop()

	return p, nil
}

// readLoop continuously reads JSONL messages from the plugin's stdout and
// routes them to the appropriate channel.
func (p *PluginProcess) readLoop() {
	defer close(p.injectCh)
	defer close(p.responseCh)

	for p.scanner.Scan() {
		var msg PluginMessage
		if err := json.Unmarshal(p.scanner.Bytes(), &msg); err != nil {
			log.Printf("plugin %s: skipping malformed JSON: %v", p.name, err)
			continue
		}

		switch msg.Type {
		case "inject_message", "log":
			select {
			case p.injectCh <- msg:
			default:
				log.Printf("plugin %s: dropped %s message (channel full)", p.name, msg.Type)
			}
		case "capabilities", "tool_result", "command_result":
			p.responseCh <- msg
		}
	}
}

// Send writes a HostMessage to the plugin's stdin as a JSONL line.
func (p *PluginProcess) Send(msg HostMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("plugin %s: process closed", p.name)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("plugin %s: marshaling message: %w", p.name, err)
	}

	data = append(data, '\n')
	if _, err := p.stdin.Write(data); err != nil {
		return fmt.Errorf("plugin %s: writing to stdin: %w", p.name, err)
	}

	return nil
}

// waitResponse waits for a response message on the response channel with a timeout.
func (p *PluginProcess) waitResponse(timeout time.Duration) (PluginMessage, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg, ok := <-p.responseCh:
		if !ok {
			return PluginMessage{}, fmt.Errorf("plugin %s: process exited", p.name)
		}
		return msg, nil
	case <-timer.C:
		return PluginMessage{}, fmt.Errorf("plugin %s: timed out waiting for response", p.name)
	}
}

// Initialize sends the initialize message and waits for a capabilities response.
func (p *PluginProcess) Initialize(cfg PluginConfig) error {
	if err := p.Send(HostMessage{
		Type:   "initialize",
		Config: &cfg,
	}); err != nil {
		return err
	}

	msg, err := p.waitResponse(initTimeout)
	if err != nil {
		return fmt.Errorf("plugin %s: initialization failed: %w", p.name, err)
	}

	if msg.Type != "capabilities" {
		return fmt.Errorf("plugin %s: expected capabilities response, got %q", p.name, msg.Type)
	}

	p.tools = msg.Tools
	p.commands = msg.Commands
	return nil
}

// ExecuteTool sends a tool_call message and waits for the tool_result response.
// Returns the result content, whether it was an error, and any communication error.
func (p *PluginProcess) ExecuteTool(id, name string, params map[string]any) (string, bool, error) {
	if err := p.Send(HostMessage{
		Type:   "tool_call",
		ID:     id,
		Name:   name,
		Params: params,
	}); err != nil {
		return "", true, err
	}

	msg, err := p.waitResponse(toolTimeout)
	if err != nil {
		return "", true, err
	}

	if msg.Type != "tool_result" {
		return "", true, fmt.Errorf("plugin %s: expected tool_result, got %q", p.name, msg.Type)
	}

	return msg.Content, msg.IsError, nil
}

// ExecuteCommand sends a command message and waits for the command_result response.
// Returns the result text, whether it was an error, and any communication error.
func (p *PluginProcess) ExecuteCommand(name, args string) (string, bool, error) {
	if err := p.Send(HostMessage{
		Type: "command",
		Name: name,
		Args: args,
	}); err != nil {
		return "", true, err
	}

	msg, err := p.waitResponse(toolTimeout)
	if err != nil {
		return "", true, err
	}

	if msg.Type != "command_result" {
		return "", true, fmt.Errorf("plugin %s: expected command_result, got %q", p.name, msg.Type)
	}

	return msg.Text, msg.IsError, nil
}

// SendEvent sends an event notification to the plugin. This is fire-and-forget;
// no response is expected.
func (p *PluginProcess) SendEvent(event EventPayload) error {
	return p.Send(HostMessage{
		Type:  "event",
		Event: &event,
	})
}

// InjectMessages returns the channel for receiving inject_message and log
// messages from the plugin.
func (p *PluginProcess) InjectMessages() <-chan PluginMessage {
	return p.injectCh
}

// Stop sends a shutdown message and waits for the plugin process to exit.
// If the process does not exit within shutdownTimeout, it is killed.
func (p *PluginProcess) Stop() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	// Best-effort shutdown message.
	_ = p.Send(HostMessage{Type: "shutdown"})
	_ = p.stdin.Close()

	// Wait for process exit with timeout.
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()

	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		// Force kill.
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return fmt.Errorf("plugin %s: killed after shutdown timeout", p.name)
	}
}

// Name returns the plugin's display name.
func (p *PluginProcess) Name() string {
	return p.name
}

// Commands returns the slash commands declared by this plugin during initialization.
func (p *PluginProcess) Commands() []CommandDef {
	return p.commands
}

// Alive returns true if the plugin process has not been marked as closed.
func (p *PluginProcess) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed
}
