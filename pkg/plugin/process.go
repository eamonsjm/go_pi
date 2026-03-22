package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"runtime/debug"
	"sync"
	"time"
)

const (
	// initTimeout is how long to wait for a plugin to respond to initialize.
	initTimeout = 10 * time.Second

	// toolTimeout is the default timeout for a tool_call execution.
	toolTimeout = 30 * time.Second

	// commandTimeout is the default timeout for a command execution.
	commandTimeout = 10 * time.Second

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
	spawnCmd     func() *exec.Cmd // creates the exec.Cmd for (re)spawning
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser // stored so we can close it to unblock readLoop
	scanner      *bufio.Scanner
	tools        []ToolDef
	commands     []CommandDef
	capabilities []string

	mu           sync.Mutex
	closed       bool
	shutdownOnce sync.Once // ensures shutdownCurrentProcess runs at most once during Stop
	readLoopWg   sync.WaitGroup // tracks readLoop goroutine lifecycle

	// injectCh receives inject_message and log messages from the background reader.
	injectCh chan PluginMessage

	// responseCh receives tool_result, command_result, and capabilities messages.
	responseCh chan PluginMessage

	// uiRequestCh receives ui_request messages from plugins (dialog/notification requests).
	uiRequestCh chan PluginMessage

	// Auto-restart fields.
	restartCfg   *RestartConfig // nil means no auto-restart
	pluginCfg    PluginConfig   // saved for re-initialization on restart
	restartCount int            // total restart attempts made
	restarting   bool           // true while restart is in progress

	// Heartbeat fields.
	heartbeatCh         chan PluginMessage // receives heartbeat_ack messages
	healthy             bool               // true when last heartbeat succeeded or no heartbeat sent yet
	lastHeartbeatAck    time.Time          // time of last successful heartbeat ack
	lastHeartbeatStatus *HeartbeatStatus   // status from last ack

	// Per-plugin timeout configuration.
	timeouts TimeoutConfig

	// Memory limit in megabytes (0 = no limit).
	memLimitMB int64

	// Supervisor fields (set when EnableAutoRestart is called).
	supervised bool
	stopCh     chan struct{} // closed to signal supervisor to stop
	stopped    chan struct{} // closed when supervisor exits
	stopErr    error         // error from the last process exit
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
		_ = stdinPipe.Close()
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	// stderr is intentionally not captured -- it goes to the host's stderr
	// for debugging purposes.

	if err := cmd.Start(); err != nil {
		_ = stdoutPipe.Close()
		_ = stdinPipe.Close()
		return nil, fmt.Errorf("starting plugin %s: %w", name, err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	p := &PluginProcess{
		name:        name,
		path:        path,
		spawnCmd:    func() *exec.Cmd { return exec.Command(path) },
		cmd:         cmd,
		stdin:       stdinPipe,
		stdout:      stdoutPipe,
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 64),
		responseCh:  make(chan PluginMessage, 16),
		uiRequestCh: make(chan PluginMessage, 16),
		heartbeatCh: make(chan PluginMessage, 4),
		healthy:     true,
		timeouts:    DefaultTimeoutConfig(),
	}

	// Start background reader that routes incoming messages.
	p.startReadLoop()

	return p, nil
}

// applyMemoryLimit applies the configured memory limit to the running process.
// Called after Start() for both initial spawn and respawn. No-op if memLimitMB
// is zero or the platform does not support rlimit.
func (p *PluginProcess) applyMemoryLimit() {
	if p.memLimitMB <= 0 {
		return
	}
	pid := p.cmd.Process.Pid
	if err := setMemoryLimit(pid, p.memLimitMB); err != nil {
		log.Printf("plugin %s: failed to set memory limit (%d MB): %v",
			p.name, p.memLimitMB, err)
	}
}

// readLoop continuously reads JSONL messages from the plugin's stdout and
// routes them to the appropriate channel. It captures channel and scanner
// references at entry so that a concurrent respawn does not cause it to
// close the wrong channels.
func (p *PluginProcess) readLoop() {
	p.mu.Lock()
	injectCh := p.injectCh
	responseCh := p.responseCh
	uiRequestCh := p.uiRequestCh
	heartbeatCh := p.heartbeatCh
	scanner := p.scanner
	p.mu.Unlock()

	// Panic recovery: prevent a panic during message parsing or channel
	// routing from crashing the host process. This defer is registered first
	// so it runs last (after channel-close defers), allowing the supervisor
	// to detect the closed channels and restart the plugin.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("plugin %s: readLoop panic (recovered): %v\n%s", p.name, r, debug.Stack())
			// Kill the subprocess so cmd.Wait() returns and the supervisor
			// can trigger a restart.
			p.mu.Lock()
			cmd := p.cmd
			p.mu.Unlock()
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}()

	defer func() {
		if injectCh != nil {
			close(injectCh)
		}
	}()
	defer func() {
		if responseCh != nil {
			close(responseCh)
		}
	}()
	defer func() {
		if uiRequestCh != nil {
			close(uiRequestCh)
		}
	}()
	defer func() {
		if heartbeatCh != nil {
			close(heartbeatCh)
		}
	}()

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
		case "ui_request":
			select {
			case uiRequestCh <- msg:
			default:
				log.Printf("plugin %s: dropped ui_request (channel full)", p.name)
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

// startReadLoop launches readLoop in a goroutine with WaitGroup tracking,
// enabling callers to wait for the goroutine to finish via readLoopWg.
func (p *PluginProcess) startReadLoop() {
	p.readLoopWg.Add(1)
	go func() {
		defer p.readLoopWg.Done()
		p.readLoop()
	}()
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

// timeoutError is returned by waitResponse when the timeout fires.
type timeoutError struct {
	plugin string
}

func (e *timeoutError) Error() string {
	return fmt.Sprintf("plugin %s: timed out waiting for response", e.plugin)
}

// waitResponse waits for a response message on the response channel with a timeout.
// If ctx is cancelled before a response arrives, ctx.Err() is returned.
func (p *PluginProcess) waitResponse(ctx context.Context, timeout time.Duration) (PluginMessage, error) {
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
		return PluginMessage{}, &timeoutError{plugin: p.name}
	case <-ctx.Done():
		return PluginMessage{}, fmt.Errorf("plugin %s: %w", p.name, ctx.Err())
	}
}

// Initialize sends the initialize message and waits for a capabilities response.
// The config is saved for automatic re-initialization on restart.
func (p *PluginProcess) Initialize(ctx context.Context, cfg PluginConfig) error {
	p.pluginCfg = cfg

	if err := p.Send(HostMessage{
		Type:   "initialize",
		Config: &cfg,
	}); err != nil {
		return fmt.Errorf("sending initialize message: %w", err)
	}

	msg, err := p.waitResponse(ctx, p.timeouts.InitTimeout)
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
// If the plugin exceeds the configured tool timeout, the process is killed.
func (p *PluginProcess) ExecuteTool(ctx context.Context, id, name string, params map[string]any) (string, bool, error) {
	if err := p.Send(HostMessage{
		Type:   "tool_call",
		ID:     id,
		Name:   name,
		Params: params,
	}); err != nil {
		return "", true, err
	}

	msg, err := p.waitResponse(ctx, p.timeouts.ToolTimeout)
	if err != nil {
		if isTimeout(err) {
			p.handleTimeout("tool_call", name, p.timeouts.ToolTimeout)
		}
		return "", true, err
	}

	if msg.Type != "tool_result" {
		return "", true, fmt.Errorf("plugin %s: expected tool_result, got %q", p.name, msg.Type)
	}

	return msg.Content, msg.IsError, nil
}

// ExecuteCommand sends a command message and waits for the command_result response.
// Returns the result text, whether it was an error, and any communication error.
// If the plugin exceeds the configured command timeout, the process is killed.
func (p *PluginProcess) ExecuteCommand(ctx context.Context, name, args string) (string, bool, error) {
	if err := p.Send(HostMessage{
		Type: "command",
		Name: name,
		Args: args,
	}); err != nil {
		return "", true, err
	}

	msg, err := p.waitResponse(ctx, p.timeouts.CommandTimeout)
	if err != nil {
		if isTimeout(err) {
			p.handleTimeout("command", name, p.timeouts.CommandTimeout)
		}
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

// UIRequests returns the channel for receiving ui_request messages from the plugin.
// Note: when a plugin restarts, this channel is closed and a new one is created
// internally; callers that need ui_request messages after restart should call
// UIRequests() again.
func (p *PluginProcess) UIRequests() <-chan PluginMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.uiRequestCh
}

// Heartbeat sends a heartbeat message and waits for a heartbeat_ack within
// the context's deadline. If the plugin responds in time, health is marked true
// and the status is stored. If the context expires first, health is marked false.
// Old plugins that don't recognize heartbeat simply won't respond; callers
// should treat the first missed heartbeat as the signal to mark unhealthy
// (the Manager handles backward compatibility by assuming healthy until the
// first heartbeat is actually sent).
func (p *PluginProcess) Heartbeat(ctx context.Context) (*HeartbeatStatus, error) {
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
	case <-ctx.Done():
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

// LastHeartbeatAck returns the time of the last successful heartbeat ack,
// or the zero time if no heartbeat has been acknowledged yet.
func (p *PluginProcess) LastHeartbeatAck() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastHeartbeatAck
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
		// Signal the supervisor to stop.
		close(p.stopCh)

		// Send shutdown to the current process. Use shutdownOnce because
		// the supervisor's ctx.Done handler may also attempt shutdown.
		p.shutdownOnce.Do(p.shutdownCurrentProcess)

		// Wait for the supervisor goroutine to finish.
		<-p.stopped
		return p.stopErr
	}

	// Unsupervised path (original behavior).
	// Capture fields under lock so concurrent goroutines (e.g. handleTimeout)
	// cannot observe a partially-torn-down state.
	p.mu.Lock()
	cmd := p.cmd
	stdin := p.stdin
	stdout := p.stdout
	p.mu.Unlock()

	// Send shutdown directly (bypass Send's closed check).
	if data, err := json.Marshal(HostMessage{Type: "shutdown"}); err != nil {
		log.Printf("plugin %s: cleanup: failed to marshal shutdown: %v", p.name, err)
	} else {
		data = append(data, '\n')
		if _, err := stdin.Write(data); err != nil {
			log.Printf("plugin %s: cleanup: failed to send shutdown: %v", p.name, err)
		}
	}
	if err := stdin.Close(); err != nil {
		log.Printf("plugin %s: cleanup: failed to close stdin: %v", p.name, err)
	}
	// Close stdout to unblock readLoop if the process doesn't exit promptly.
	if stdout != nil {
		if err := stdout.Close(); err != nil {
			log.Printf("plugin %s: cleanup: failed to close stdout: %v", p.name, err)
		}
	}

	// Wait for readLoop goroutine to finish and close all channels before
	// proceeding to reap the subprocess.
	p.readLoopWg.Wait()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("plugin %s: process exited: %w", p.name, err)
		}
		return nil
	case <-timer.C:
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				log.Printf("plugin %s: cleanup: failed to kill process after shutdown timeout: %v", p.name, err)
			}
		}
		// Drain the Wait goroutine to prevent a leak after Kill.
		<-done
		return fmt.Errorf("plugin %s: killed after shutdown timeout", p.name)
	}
}

// shutdownCurrentProcess sends a shutdown message and closes stdin for the
// current process. Used by the supervised Stop path.
func (p *PluginProcess) shutdownCurrentProcess() {
	p.mu.Lock()
	stdin := p.stdin
	stdout := p.stdout
	p.mu.Unlock()

	data, err := json.Marshal(HostMessage{Type: "shutdown"})
	if err != nil {
		log.Printf("plugin %s: failed to marshal shutdown message: %v", p.name, err)
	} else {
		data = append(data, '\n')
		if _, err := stdin.Write(data); err != nil {
			log.Printf("plugin %s: failed to write shutdown message: %v", p.name, err)
		}
	}
	if err := stdin.Close(); err != nil {
		log.Printf("plugin %s: cleanup: failed to close stdin: %v", p.name, err)
	}
	// Close stdout to unblock readLoop if the process doesn't exit promptly.
	if stdout != nil {
		if err := stdout.Close(); err != nil {
			log.Printf("plugin %s: cleanup: failed to close stdout: %v", p.name, err)
		}
	}
}

// isTimeout returns true if the error is a timeout from waitResponse.
func isTimeout(err error) bool {
	_, ok := err.(*timeoutError)
	return ok
}

// handleTimeout logs a timeout event and kills the plugin process. If
// auto-restart is enabled, the supervisor will handle respawning.
func (p *PluginProcess) handleTimeout(opType, opName string, timeout time.Duration) {
	log.Printf("plugin %s: timeout after %v on %s %q — killing process",
		p.name, timeout, opType, opName)

	p.mu.Lock()
	cmd := p.cmd
	supervised := p.supervised
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	if supervised {
		// Kill the process; the supervisor loop will detect the exit
		// and handle restart.
		if err := cmd.Process.Kill(); err != nil {
			log.Printf("plugin %s: cleanup: failed to kill process after timeout: %v", p.name, err)
		}
		return
	}

	// Unsupervised: kill the process directly.
	if err := cmd.Process.Kill(); err != nil {
		log.Printf("plugin %s: cleanup: failed to kill process after timeout: %v", p.name, err)
	}
}

// SetTimeouts configures per-plugin timeouts. Any zero-value field in cfg
// keeps the current value.
func (p *PluginProcess) SetTimeouts(cfg TimeoutConfig) {
	if cfg.InitTimeout > 0 {
		p.timeouts.InitTimeout = cfg.InitTimeout
	}
	if cfg.ToolTimeout > 0 {
		p.timeouts.ToolTimeout = cfg.ToolTimeout
	}
	if cfg.CommandTimeout > 0 {
		p.timeouts.CommandTimeout = cfg.CommandTimeout
	}
}

// SetMemoryLimit sets the memory limit in MB for the plugin process. Must be
// called before the process is started for it to take effect on spawn. On
// restart, the limit is re-applied to the new process.
func (p *PluginProcess) SetMemoryLimit(limitMB int64) {
	p.memLimitMB = limitMB
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
	p.stopCh = make(chan struct{})
	p.stopped = make(chan struct{})

	go p.waitAndSupervise(p.stopCh)
}

// waitAndSupervise is the supervisor goroutine. It calls cmd.Wait() in a loop,
// restarting the process on unexpected exit with exponential backoff. It is the
// sole owner of cmd.Wait() — no other code should call it when supervised.
func (p *PluginProcess) waitAndSupervise(stopCh <-chan struct{}) {
	defer close(p.stopped)
	defer p.readLoopWg.Wait() // ensure readLoop is done before signaling stopped

	// Create a context that cancels when stopCh closes, so respawn's
	// Initialize call is cancelled during shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()

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

			// Wait for backoff or stop signal.
			select {
			case <-time.After(backoff):
			case <-stopCh:
				p.mu.Lock()
				p.restarting = false
				p.mu.Unlock()
				return
			}

			if err := p.respawn(ctx); err != nil {
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

		case <-stopCh:
			// Stop requested while process is still running.
			// Shutdown the current process and wait for it to exit.
			// Use shutdownOnce to avoid racing with Stop() which may
			// have already called shutdownCurrentProcess.
			p.shutdownOnce.Do(p.shutdownCurrentProcess)

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
				if cmd != nil && cmd.Process != nil {
					if err := cmd.Process.Kill(); err != nil {
						log.Printf("plugin %s: cleanup: failed to kill process during shutdown: %v", p.name, err)
					}
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
func (p *PluginProcess) respawn(ctx context.Context) error {
	// Ensure the previous readLoop goroutine has fully exited before
	// replacing channels and starting a new one.
	p.readLoopWg.Wait()

	cmd := p.spawnCmd()

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		if closeErr := stdinPipe.Close(); closeErr != nil {
			log.Printf("plugin %s: cleanup: failed to close stdin pipe: %v", p.name, closeErr)
		}
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if closeErr := stdinPipe.Close(); closeErr != nil {
			log.Printf("plugin %s: cleanup: failed to close stdin pipe: %v", p.name, closeErr)
		}
		if closeErr := stdoutPipe.Close(); closeErr != nil {
			log.Printf("plugin %s: cleanup: failed to close stdout pipe: %v", p.name, closeErr)
		}
		return fmt.Errorf("starting plugin: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	newInjectCh := make(chan PluginMessage, 64)
	newResponseCh := make(chan PluginMessage, 16)
	newUIRequestCh := make(chan PluginMessage, 16)
	newHeartbeatCh := make(chan PluginMessage, 4)

	p.mu.Lock()
	p.cmd = cmd
	p.stdin = stdinPipe
	p.stdout = stdoutPipe
	p.scanner = scanner
	p.injectCh = newInjectCh
	p.responseCh = newResponseCh
	p.uiRequestCh = newUIRequestCh
	p.heartbeatCh = newHeartbeatCh
	p.healthy = true // Reset health on restart.
	p.closed = false // Allow Send to work again for initialization.
	p.startReadLoop()
	p.mu.Unlock()

	// Re-apply memory limit on the new process.
	p.applyMemoryLimit()

	// Re-initialize the plugin with the saved config.
	if err := p.Initialize(ctx, p.pluginCfg); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			log.Printf("plugin %s: cleanup: failed to kill process after re-init failure: %v", p.name, killErr)
		}
		// Close stdout to unblock readLoop's scanner.Scan() — without this,
		// readLoop blocks on I/O and cmd.Wait() below cannot return (it waits
		// for all pipe I/O to complete), leaking the Wait goroutine.
		p.mu.Lock()
		if p.stdout != nil {
			if closeErr := p.stdout.Close(); closeErr != nil {
				log.Printf("plugin %s: cleanup: failed to close stdout after re-init failure: %v", p.name, closeErr)
			}
		}
		p.mu.Unlock()
		// Reap the killed process so the supervisor loop doesn't spawn a
		// Wait() goroutine on a zombie. Without this, the next iteration's
		// goroutine could block if the process is slow to exit, and leak
		// if ctx cancels before it returns.
		_ = cmd.Wait()
		return fmt.Errorf("re-initialization failed: %w", err)
	}

	return nil
}

// WaitUIRequest waits for a ui_request message from the plugin.
// Returns the UI request message from the plugin or an error if the context
// expires first.
func (p *PluginProcess) WaitUIRequest(ctx context.Context) (PluginMessage, error) {
	p.mu.Lock()
	uiRequestCh := p.uiRequestCh
	p.mu.Unlock()

	select {
	case msg, ok := <-uiRequestCh:
		if !ok {
			return PluginMessage{}, fmt.Errorf("plugin %s: process exited", p.name)
		}
		return msg, nil
	case <-ctx.Done():
		return PluginMessage{}, &timeoutError{plugin: p.name}
	}
}

// RespondToUIRequest sends a ui_response to a ui_request from the plugin.
// The response includes the user's input/selection and an optional error message.
func (p *PluginProcess) RespondToUIRequest(id string, value string, closed bool, errMsg string) error {
	if err := p.Send(HostMessage{
		Type: "ui_response",
		UIResponse: &UIResponse{
			ID:     id,
			Value:  value,
			Closed: closed,
			Error:  errMsg,
		},
	}); err != nil {
		return fmt.Errorf("plugin %s: sending UI response: %w", p.name, err)
	}
	return nil
}
