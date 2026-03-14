package plugin

import (
	"bufio"
	"context"
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

	// Default restart configuration values.
	defaultMaxRestartAttempts    = 5
	defaultInitialRestartBackoff = 1 * time.Second
	defaultMaxRestartBackoff     = 30 * time.Second

	// Default heartbeat configuration values.
	defaultHeartbeatInterval = 10 * time.Second
	defaultHeartbeatTimeout  = 5 * time.Second
)

// RestartConfig controls auto-restart behavior for crashed plugins.
type RestartConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// DefaultRestartConfig returns a RestartConfig with default values.
func DefaultRestartConfig() RestartConfig {
	return RestartConfig{
		MaxAttempts:    defaultMaxRestartAttempts,
		InitialBackoff: defaultInitialRestartBackoff,
		MaxBackoff:     defaultMaxRestartBackoff,
	}
}

// DefaultHeartbeatConfig returns a HeartbeatConfig with default values.
func DefaultHeartbeatConfig() HeartbeatConfig {
	return HeartbeatConfig{
		Interval: defaultHeartbeatInterval,
		Timeout:  defaultHeartbeatTimeout,
	}
}

// PluginProcess manages a single plugin subprocess and its JSONL communication.
type PluginProcess struct {
	name         string
	path         string
	args         []string // arguments for subprocess (not including path)
	env          []string // environment for subprocess (nil = inherit parent)
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

	// Auto-restart fields.
	restartCfg   *RestartConfig // nil means no auto-restart
	pluginCfg    PluginConfig   // saved for re-initialization on restart
	restartCount int            // total restart attempts made
	restarting   bool           // true while restart is in progress

	// Heartbeat fields.
	heartbeatCh      chan PluginMessage // receives heartbeat_ack messages
	healthy          bool              // true when last heartbeat succeeded or no heartbeat sent yet
	lastHeartbeatAck time.Time         // time of last successful heartbeat ack
	lastHeartbeatStatus *HeartbeatStatus // status from last ack

	// Supervisor fields (set when EnableAutoRestart is called).
	supervised bool
	ctx        context.Context
	cancel     context.CancelFunc
	stopped    chan struct{} // closed when supervisor exits
	stopErr    error        // error from the last process exit
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
		name:        name,
		path:        path,
		cmd:         cmd,
		stdin:       stdinPipe,
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 64),
		responseCh:  make(chan PluginMessage, 16),
		heartbeatCh: make(chan PluginMessage, 4),
		healthy:     true,
	}

	// Start background reader that routes incoming messages.
	go p.readLoop()

	return p, nil
}

// readLoop continuously reads JSONL messages from the plugin's stdout and
// routes them to the appropriate channel. It captures channel and scanner
// references at entry so that a concurrent respawn does not cause it to
// close the wrong channels.
func (p *PluginProcess) readLoop() {
	p.mu.Lock()
	injectCh := p.injectCh
	responseCh := p.responseCh
	heartbeatCh := p.heartbeatCh
	scanner := p.scanner
	p.mu.Unlock()

	defer close(injectCh)
	defer close(responseCh)
	defer close(heartbeatCh)

	for scanner.Scan() {
		var msg PluginMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			log.Printf("plugin %s: skipping malformed JSON: %v", p.name, err)
			continue
		}

		switch msg.Type {
		case "inject_message", "log":
			select {
			case injectCh <- msg:
			default:
				log.Printf("plugin %s: dropped %s message (channel full)", p.name, msg.Type)
			}
		case "capabilities", "tool_result", "command_result":
			select {
			case responseCh <- msg:
			default:
				log.Printf("plugin %s: dropped %s response (channel full)", p.name, msg.Type)
			}
		case "heartbeat_ack":
			select {
			case heartbeatCh <- msg:
			default:
				log.Printf("plugin %s: dropped heartbeat_ack (channel full)", p.name)
			}
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
	p.mu.Lock()
	responseCh := p.responseCh
	p.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg, ok := <-responseCh:
		if !ok {
			return PluginMessage{}, fmt.Errorf("plugin %s: process exited", p.name)
		}
		return msg, nil
	case <-timer.C:
		return PluginMessage{}, fmt.Errorf("plugin %s: timed out waiting for response", p.name)
	}
}

// Initialize sends the initialize message and waits for a capabilities response.
// The config is saved for automatic re-initialization on restart.
func (p *PluginProcess) Initialize(cfg PluginConfig) error {
	p.pluginCfg = cfg

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
// messages from the plugin. Note: when a plugin restarts, this channel is
// closed and a new one is created internally; callers that need inject
// messages after restart should call InjectMessages() again.
func (p *PluginProcess) InjectMessages() <-chan PluginMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.injectCh
}

// Heartbeat sends a heartbeat message and waits for a heartbeat_ack within
// the given timeout. If the plugin responds in time, health is marked true and
// the status is stored. If the plugin does not respond, health is marked false.
// Old plugins that don't recognize heartbeat simply won't respond; callers
// should treat the first missed heartbeat as the signal to mark unhealthy
// (the Manager handles backward compatibility by assuming healthy until the
// first heartbeat is actually sent).
func (p *PluginProcess) Heartbeat(timeout time.Duration) (*HeartbeatStatus, error) {
	p.mu.Lock()
	if p.closed || p.restarting {
		p.mu.Unlock()
		return nil, fmt.Errorf("plugin %s: not running", p.name)
	}
	heartbeatCh := p.heartbeatCh
	p.mu.Unlock()

	if err := p.Send(HostMessage{Type: "heartbeat"}); err != nil {
		p.mu.Lock()
		p.healthy = false
		p.mu.Unlock()
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg, ok := <-heartbeatCh:
		if !ok {
			p.mu.Lock()
			p.healthy = false
			p.mu.Unlock()
			return nil, fmt.Errorf("plugin %s: process exited", p.name)
		}
		p.mu.Lock()
		p.healthy = true
		p.lastHeartbeatAck = time.Now()
		p.lastHeartbeatStatus = msg.Status
		p.mu.Unlock()
		return msg.Status, nil
	case <-timer.C:
		p.mu.Lock()
		p.healthy = false
		p.mu.Unlock()
		return nil, fmt.Errorf("plugin %s: heartbeat timeout", p.name)
	}
}

// Healthy returns whether the plugin's last heartbeat succeeded. A plugin
// that has never been heartbeated is considered healthy.
func (p *PluginProcess) Healthy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthy
}

// LastHeartbeatStatus returns the status from the last successful heartbeat ack,
// or nil if no heartbeat has been acknowledged yet.
func (p *PluginProcess) LastHeartbeatStatus() *HeartbeatStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastHeartbeatStatus
}

// Stop sends a shutdown message and waits for the plugin process to exit.
// If the process does not exit within shutdownTimeout, it is killed.
// When auto-restart is enabled, Stop cancels any pending restarts first.
func (p *PluginProcess) Stop() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	supervised := p.supervised
	p.mu.Unlock()

	if supervised {
		// Cancel pending restarts and signal the supervisor to stop.
		p.cancel()

		// Send shutdown to the current process.
		p.shutdownCurrentProcess()

		// Wait for the supervisor goroutine to finish.
		<-p.stopped
		return p.stopErr
	}

	// Unsupervised path (original behavior).
	_ = p.sendShutdownDirect()
	_ = p.stdin.Close()

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
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return fmt.Errorf("plugin %s: killed after shutdown timeout", p.name)
	}
}

// sendShutdownDirect writes a shutdown message directly to stdin, bypassing
// the Send method's closed check. Used during Stop when p.closed is already true.
func (p *PluginProcess) sendShutdownDirect() error {
	data, err := json.Marshal(HostMessage{Type: "shutdown"})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = p.stdin.Write(data)
	return err
}

// shutdownCurrentProcess sends a shutdown message and closes stdin for the
// current process. Used by the supervised Stop path.
func (p *PluginProcess) shutdownCurrentProcess() {
	p.mu.Lock()
	stdin := p.stdin
	p.mu.Unlock()

	data, _ := json.Marshal(HostMessage{Type: "shutdown"})
	data = append(data, '\n')
	_, _ = stdin.Write(data)
	_ = stdin.Close()
}

// Name returns the plugin's display name.
func (p *PluginProcess) Name() string {
	return p.name
}

// Commands returns the slash commands declared by this plugin during initialization.
func (p *PluginProcess) Commands() []CommandDef {
	return p.commands
}

// Alive returns true if the plugin process is running and not in the middle
// of a restart.
func (p *PluginProcess) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && !p.restarting
}

// Restarting returns true if the plugin is currently restarting after a crash.
func (p *PluginProcess) Restarting() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restarting
}

// RestartCount returns the number of restart attempts that have been made.
func (p *PluginProcess) RestartCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restartCount
}

// EnableAutoRestart enables automatic restart with the given configuration.
// A supervisor goroutine is started that monitors the process and handles
// restarts with exponential backoff. Must be called after Initialize.
func (p *PluginProcess) EnableAutoRestart(cfg RestartConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cfgCopy := cfg
	p.restartCfg = &cfgCopy
	p.supervised = true
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.stopped = make(chan struct{})

	go p.waitAndSupervise()
}

// waitAndSupervise is the supervisor goroutine. It calls cmd.Wait() in a loop,
// restarting the process on unexpected exit with exponential backoff. It is the
// sole owner of cmd.Wait() — no other code should call it when supervised.
func (p *PluginProcess) waitAndSupervise() {
	defer close(p.stopped)

	for {
		p.mu.Lock()
		cmd := p.cmd
		p.mu.Unlock()

		// Wait for the current process to exit.
		waitErr := make(chan error, 1)
		go func() {
			waitErr <- cmd.Wait()
		}()

		select {
		case err := <-waitErr:
			// Process exited.
			p.mu.Lock()
			if p.closed {
				// Clean shutdown — Stop() was called.
				p.stopErr = err
				p.mu.Unlock()
				return
			}

			if p.restartCfg == nil {
				p.closed = true
				p.mu.Unlock()
				return
			}

			p.restartCount++
			count := p.restartCount
			maxAttempts := p.restartCfg.MaxAttempts

			if count > maxAttempts {
				log.Printf("plugin %s: giving up after %d restart attempts", p.name, maxAttempts)
				p.closed = true
				p.restarting = false
				p.mu.Unlock()
				return
			}

			p.restarting = true

			// Compute backoff: initialBackoff * 2^(count-1), capped at maxBackoff.
			backoff := p.restartCfg.InitialBackoff * time.Duration(1<<uint(count-1))
			if backoff > p.restartCfg.MaxBackoff {
				backoff = p.restartCfg.MaxBackoff
			}
			p.mu.Unlock()

			log.Printf("plugin %s: crashed (exit: %v), restarting in %v (attempt %d/%d)",
				p.name, err, backoff, count, maxAttempts)

			// Wait for backoff or cancellation.
			select {
			case <-time.After(backoff):
			case <-p.ctx.Done():
				p.mu.Lock()
				p.restarting = false
				p.mu.Unlock()
				return
			}

			if err := p.respawn(); err != nil {
				log.Printf("plugin %s: restart attempt %d failed: %v", p.name, count, err)
				// Loop continues — restartCount already incremented, will try again
				// or give up if max attempts reached.
				continue
			}

			log.Printf("plugin %s: restarted successfully (attempt %d/%d)", p.name, count, maxAttempts)
			p.mu.Lock()
			p.restarting = false
			p.mu.Unlock()
			// Loop back to wait on the new process.

		case <-p.ctx.Done():
			// Stop requested while process is still running.
			// Shutdown the current process and wait for it to exit.
			p.shutdownCurrentProcess()

			timer := time.NewTimer(shutdownTimeout)
			select {
			case err := <-waitErr:
				timer.Stop()
				p.stopErr = err
			case <-timer.C:
				timer.Stop()
				p.mu.Lock()
				cmd := p.cmd
				p.mu.Unlock()
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				p.stopErr = <-waitErr
			}
			return
		}
	}
}

// respawn creates a new plugin subprocess, sets up communication channels,
// and re-initializes the plugin. The old channels must already be closed
// (by readLoop exiting) before calling this.
func (p *PluginProcess) respawn() error {
	cmd := exec.Command(p.path, p.args...)
	cmd.Env = p.env

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting plugin: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	newInjectCh := make(chan PluginMessage, 64)
	newResponseCh := make(chan PluginMessage, 16)
	newHeartbeatCh := make(chan PluginMessage, 4)

	p.mu.Lock()
	p.cmd = cmd
	p.stdin = stdinPipe
	p.scanner = scanner
	p.injectCh = newInjectCh
	p.responseCh = newResponseCh
	p.heartbeatCh = newHeartbeatCh
	p.healthy = true // Reset health on restart.
	p.closed = false // Allow Send to work again for initialization.
	p.mu.Unlock()

	go p.readLoop()

	// Re-initialize the plugin with the saved config.
	if err := p.Initialize(p.pluginCfg); err != nil {
		_ = cmd.Process.Kill()
		// Don't call Wait here — the supervisor loop handles it.
		return fmt.Errorf("re-initialization failed: %w", err)
	}

	return nil
}
