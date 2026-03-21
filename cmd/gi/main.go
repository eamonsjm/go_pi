package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/auth"
	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/plugin"
	"github.com/ejm/go_pi/pkg/rpc"
	"github.com/ejm/go_pi/pkg/session"
	"github.com/ejm/go_pi/pkg/tools"
	"github.com/ejm/go_pi/pkg/tui"
)

func main() {
	// Flag variables (use 'Flag' suffix to avoid conflicts with later variable names)
	var modelVal string
	var providerVal string
	var thinkingVal string
	var printVal string
	var sessionVal string
	var newVal bool
	var cwdVal string
	var jsonVal bool
	var rpcVal bool
	var pluginVal string

	// Register flags with both short and long forms
	flag.StringVar(&modelVal, "m", "", "Model name (short form, same as -model)")
	flag.StringVar(&modelVal, "model", "", "Model to use (e.g. claude-opus-4-6)")

	flag.StringVar(&providerVal, "p", "", "Provider name (short form, same as -provider)")
	flag.StringVar(&providerVal, "provider", "", "Provider (anthropic, openai, etc.)")

	flag.StringVar(&thinkingVal, "t", "", "Thinking level (short form, same as -thinking)")
	flag.StringVar(&thinkingVal, "thinking", "", "Thinking level (off, low, medium, high)")

	flag.StringVar(&printVal, "P", "", "Print mode: send prompt and print response (short form, same as -print)")
	flag.StringVar(&printVal, "print", "", "Print mode: send prompt and print response")

	flag.StringVar(&sessionVal, "s", "", "Session ID (short form, same as -session)")
	flag.StringVar(&sessionVal, "session", "", "Resume a session by ID")

	flag.BoolVar(&newVal, "n", false, "Start fresh session (short form, same as -new)")
	flag.BoolVar(&newVal, "new", false, "Start a fresh session instead of resuming")

	flag.StringVar(&cwdVal, "w", "", "Working directory (short form, same as -cwd)")
	flag.StringVar(&cwdVal, "cwd", "", "Working directory")

	flag.BoolVar(&jsonVal, "j", false, "JSON mode (short form, same as -json)")
	flag.BoolVar(&jsonVal, "json", false, "JSON event stream output mode")

	flag.BoolVar(&rpcVal, "r", false, "RPC mode (short form, same as -rpc)")
	flag.BoolVar(&rpcVal, "rpc", false, "JSON-RPC 2.0 mode over stdin/stdout")

	flag.StringVar(&pluginVal, "plugin", "", "Comma-separated paths to plugin executables or directories")

	flag.Parse()

	// Create pointers for rest of code compatibility
	modelFlag := &modelVal
	providerFlag := &providerVal
	thinkingFlag := &thinkingVal
	printFlag := &printVal
	sessionFlag := &sessionVal
	newFlag := &newVal
	cwdFlag := &cwdVal
	jsonFlag := &jsonVal
	rpcFlag := &rpcVal
	pluginFlag := &pluginVal

	// Check if the first positional arg is a known model name (e.g. `gi claude-haiku-4-5-20251001`).
	args := flag.Args()
	if len(args) > 0 && *modelFlag == "" {
		if opt, ok := tui.ResolveModelArg(args[0]); ok {
			modelFlag = &opt.Model
			providerFlag = &opt.Provider
			args = args[1:]
		}
	}

	// Process @filepath arguments from remaining CLI args.
	initialPrompt, err := processFileArgs(args)
	if err != nil {
		log.Fatalf("Error processing arguments: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if *modelFlag != "" {
		cfg.DefaultModel = *modelFlag
	}
	if *providerFlag != "" {
		cfg.DefaultProvider = *providerFlag
	}
	if *thinkingFlag != "" {
		cfg.ThinkingLevel = *thinkingFlag
	}

	// Initialize theme from config (must happen before TUI creation).
	if theme, err := tui.ResolveTheme(cfg.Theme); err == nil {
		tui.SetTheme(theme)
	}

	if *cwdFlag != "" {
		if err := os.Chdir(*cwdFlag); err != nil {
			log.Fatalf("Failed to change directory: %v", err)
		}
	}

	registry := tools.NewRegistry()
	tools.RegisterDefaults(registry)

	pluginMgr := plugin.NewManager(registry)
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	if err := pluginMgr.Discover([]string{
		filepath.Join(home, ".gi", "plugins"),
		filepath.Join(cwd, ".gi", "plugins"),
	}); err != nil {
		log.Printf("Failed to discover plugins: %v", err)
	}
	if *pluginFlag != "" {
		for _, p := range strings.Split(*pluginFlag, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if err := pluginMgr.LoadPlugin(p); err != nil {
				log.Printf("Failed to load plugin %s: %v", p, err)
			}
		}
	}
	if err := pluginMgr.Initialize(plugin.PluginConfig{
		Cwd:       cwd,
		Model:     cfg.DefaultModel,
		Provider:  cfg.DefaultProvider,
		GiVersion: "0.1.0",
	}); err != nil {
		log.Printf("Failed to initialize plugins: %v", err)
	}
	defer func() {
		if err := pluginMgr.Shutdown(); err != nil {
			log.Printf("Failed to shutdown plugins: %v", err)
		}
	}()

	sessionDir := cfg.SessionDir
	if sessionDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Printf("Warning: could not determine home directory: %v", err)
			home = ""
		}
		if home != "" {
			sessionDir = filepath.Join(home, ".gi", "sessions")
		} else {
			sessionDir = ".gi/sessions"
		}
	}
	sessionMgr := session.NewManager(sessionDir)

	authStore, authResolver, authErr := setupAuth()
	if authErr != nil {
		log.Fatalf("Failed to initialize auth: %v", authErr)
	}

	// Resolve provider (may fail if no API key — that's ok for interactive mode)
	provider, providerErr := resolveProvider(cfg, authResolver)

	// Print mode requires a working provider
	if *printFlag != "" {
		if providerErr != nil {
			log.Fatalf("Cannot use print mode: %v", providerErr)
		}
		agentLoop := makeAgentLoop(provider, registry, cfg)
		sessionMgr.NewSession()
		prompt := *printFlag
		if initialPrompt != "" {
			prompt = initialPrompt + "\n\n" + prompt
		}
		runPrintMode(agentLoop, sessionMgr, prompt)
		return
	}

	// JSON event stream mode
	if *jsonFlag {
		if providerErr != nil {
			log.Fatalf("Cannot use JSON mode: %v", providerErr)
		}
		agentLoop := makeAgentLoop(provider, registry, cfg)
		rpc.RunJSONStream(agentLoop, initialPrompt)
		return
	}

	// JSON-RPC 2.0 mode
	if *rpcFlag {
		if providerErr != nil {
			log.Fatalf("Cannot use RPC mode: %v", providerErr)
		}
		agentLoop := makeAgentLoop(provider, registry, cfg)
		rpc.RunRPC(agentLoop)
		return
	}

	// Create agent loop - may be nil provider if no API key configured
	var agentLoop *agent.AgentLoop
	if provider != nil {
		agentLoop = makeAgentLoop(provider, registry, cfg)
	} else {
		// Create a placeholder loop with no provider — submitting will show the error
		agentLoop = agent.NewAgentLoop(
			nil,
			registry,
			agent.WithModel(cfg.DefaultModel),
			agent.WithMaxTokens(cfg.MaxTokens),
			agent.WithThinking(ai.ThinkingLevel(cfg.ThinkingLevel)),
			agent.WithSystemPrompt(buildSystemPrompt()),
		)
	}

	// Restore or create session.
	var restoredMsgs []ai.Message
	var restoredSessionID string

	if *sessionFlag != "" {
		// Explicit --session flag: load the specified session.
		if err := sessionMgr.LoadSession(*sessionFlag); err != nil {
			log.Fatalf("Failed to load session: %v", err)
		}
		restoredMsgs = sessionMgr.GetMessages()
		restoredSessionID = sessionMgr.CurrentID()
		agentLoop.SetMessages(restoredMsgs)
	} else if *newFlag {
		// Explicit --new flag: start fresh.
		sessionMgr.NewSession()
	} else {
		// Auto-resume the most recent session.
		if latest := sessionMgr.LatestSessionID(); latest != "" {
			if err := sessionMgr.LoadSession(latest); err == nil {
				restoredMsgs = sessionMgr.GetMessages()
				restoredSessionID = latest
				agentLoop.SetMessages(restoredMsgs)
			} else {
				// Failed to load — start fresh.
				sessionMgr.NewSession()
			}
		} else {
			sessionMgr.NewSession()
		}
	}

	runInteractive(agentLoop, sessionMgr, cfg, providerErr, pluginMgr, authStore, authResolver, restoredSessionID, restoredMsgs, initialPrompt)
}

// setupAuth creates the auth store and resolver with registered OAuth providers.
func setupAuth() (*auth.Store, *auth.Resolver, error) {
	store, err := auth.NewStore("")
	if err != nil {
		return nil, nil, err
	}
	if err := store.Load(); err != nil {
		return nil, nil, err
	}

	resolver := auth.NewResolver(store)
	resolver.RegisterProvider(auth.NewAnthropicOAuth())
	resolver.RegisterProvider(auth.NewOpenAIOAuth())

	return store, resolver, nil
}

func resolveProvider(cfg *config.Config, resolver *auth.Resolver) (ai.Provider, error) {
	providerName := cfg.DefaultProvider
	if providerName == "" {
		// Auto-detect based on available credentials.
		for _, name := range []string{"anthropic", "openrouter", "openai", "gemini", "azure"} {
			key, _ := resolver.Resolve(name)
			if key != "" {
				providerName = name
				break
			}
		}
		// Bedrock uses AWS credential chain, not API keys.
		if providerName == "" && os.Getenv("AWS_ACCESS_KEY_ID") != "" {
			providerName = "bedrock"
		}
		// Ollama needs no API key — just a reachable host.
		if providerName == "" && os.Getenv("OLLAMA_HOST") != "" {
			providerName = "ollama"
		}
		if providerName == "" {
			return nil, fmt.Errorf("no API key found. Set ANTHROPIC_API_KEY, OPENROUTER_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY, AZURE_OPENAI_API_KEY, AWS credentials, or OLLAMA_HOST, or use /login <provider>")
		}
	}

	model := cfg.DefaultModel
	if model == "" {
		switch providerName {
		case "anthropic":
			model = "claude-opus-4-6"
		case "openrouter":
			model = "anthropic/claude-opus-4-6"
		case "openai":
			model = "gpt-4o"
		case "gemini":
			model = "gemini-2.0-flash"
		case "azure":
			model = "gpt-4o" // Azure deployment determines actual model
		case "bedrock":
			model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
		case "ollama":
			model = "llama3.2"
		}
		cfg.DefaultModel = model
	}

	// Bedrock uses AWS credential chain, not API keys.
	if providerName == "bedrock" {
		return ai.NewBedrockProvider(os.Getenv("AWS_REGION"))
	}

	// Ollama is local and needs no API key — just a host URL.
	if providerName == "ollama" {
		return ai.NewOllamaProvider(os.Getenv("OLLAMA_HOST"))
	}

	key, err := resolver.Resolve(providerName)
	if err != nil {
		return nil, fmt.Errorf("resolve %s credentials: %w", providerName, err)
	}
	if key == "" {
		return nil, fmt.Errorf("%s: no API key configured (set env var or use /login %s)", providerName, providerName)
	}

	switch providerName {
	case "anthropic":
		if resolver.IsOAuthToken(providerName) {
			return ai.NewAnthropicProviderWithToken(key)
		}
		return ai.NewAnthropicProvider(key)
	case "openrouter":
		return ai.NewOpenRouterProvider(key)
	case "openai":
		return ai.NewOpenAIProvider(key)
	case "gemini":
		return ai.NewGeminiProvider(key)
	case "azure":
		return ai.NewAzureOpenAIProvider(key, "", "")
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
}

func buildSystemPrompt() string {
	base := "You are Claude Code, Anthropic's official CLI for Claude."

	var parts []string
	seenContent := make(map[string]bool)

	// Walk directory tree from current to root, collecting files
	cwd, err := os.Getwd()
	if err != nil {
		return base
	}

	// Check .claude/SYSTEM.md in current directory
	dotClaudePath := filepath.Join(cwd, ".claude", "SYSTEM.md")
	if data, err := os.ReadFile(dotClaudePath); err == nil {
		content := string(data)
		if !seenContent[content] {
			parts = append(parts, content)
			seenContent[content] = true
		}
	}

	// Walk from current directory up to root
	current := cwd
	for {
		// Check CLAUDE.md (deepest first - we're already starting from deepest)
		claudePath := filepath.Join(current, "CLAUDE.md")
		if data, err := os.ReadFile(claudePath); err == nil {
			content := string(data)
			if !seenContent[content] {
				parts = append(parts, content)
				seenContent[content] = true
			}
		}

		// Check AGENTS.md
		agentsPath := filepath.Join(current, "AGENTS.md")
		if data, err := os.ReadFile(agentsPath); err == nil {
			content := string(data)
			if !seenContent[content] {
				parts = append(parts, content)
				seenContent[content] = true
			}
		}

		// Check APPEND_SYSTEM.md
		appendPath := filepath.Join(current, "APPEND_SYSTEM.md")
		if data, err := os.ReadFile(appendPath); err == nil {
			content := string(data)
			if !seenContent[content] {
				parts = append(parts, content)
				seenContent[content] = true
			}
		}

		// Move to parent directory
		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root
			break
		}
		current = parent
	}

	if len(parts) == 0 {
		return base
	}

	return base + "\n\n" + strings.Join(parts, "\n\n")
}

func runPrintMode(agentLoop *agent.AgentLoop, sessionMgr *session.Manager, prompt string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	events := agentLoop.Events()
	go func() {
		if err := agentLoop.Prompt(ctx, prompt); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}()

	for event := range events {
		switch event.Type {
		case agent.EventAssistantText:
			fmt.Print(event.Delta)
		case agent.EventAgentEnd:
			fmt.Println()
			return
		case agent.EventAgentError:
			fmt.Fprintf(os.Stderr, "\nError: %v\n", event.Error)
			os.Exit(1)
		}
	}
}

func makeAgentLoop(provider ai.Provider, registry *tools.Registry, cfg *config.Config) *agent.AgentLoop {
	return agent.NewAgentLoop(
		provider,
		registry,
		agent.WithModel(cfg.DefaultModel),
		agent.WithMaxTokens(cfg.MaxTokens),
		agent.WithThinking(ai.ThinkingLevel(cfg.ThinkingLevel)),
		agent.WithSystemPrompt(buildSystemPrompt()),
	)
}

func runInteractive(agentLoop *agent.AgentLoop, sessionMgr *session.Manager, cfg *config.Config, providerErr error, pluginMgr *plugin.Manager, authStore *auth.Store, authResolver *auth.Resolver, restoredSessionID string, restoredMsgs []ai.Message, initialPrompt string) {
	// Create the application lifecycle context. This is cancelled when
	// runInteractive returns, ensuring all background operations (such as
	// compaction) are stopped when the user quits.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := tui.NewApp()
	app.SetHasUI(true) // Interactive mode has UI
	app.SetModel(cfg.DefaultModel)
	app.SetThinking(cfg.ThinkingLevel)
	app.RegisterBuiltinCommands(ctx, agentLoop, sessionMgr, cfg, authStore, authResolver)
	app.SetModelChangeCallback(func(provider, model string) {
		agentLoop.SetModel(model)
		// Persist the model selection to config
		cfg.DefaultModel = model
		if provider != "" {
			cfg.DefaultProvider = provider
		}
		if err := cfg.Save(); err != nil {
			log.Printf("Failed to save model change to config: %v", err)
		}
	})
	app.SetLoginSuccessCallback(func(providerName string) {
		p, err := resolveProvider(cfg, authResolver)
		if err != nil {
			log.Printf("Failed to resolve provider after login: %v", err)
			return
		}
		agentLoop.SetProvider(p)
		agentLoop.SetModel(cfg.DefaultModel)
		app.SetModel(cfg.DefaultModel)
	})

	// Register plugin-provided slash commands.
	for _, proc := range pluginMgr.Plugins() {
		for _, cmdDef := range proc.Commands() {
			app.RegisterCommand(&tui.SlashCommand{
				Name:        cmdDef.Name,
				Description: cmdDef.Description,
				Execute: func(args string) tea.Cmd {
					return func() tea.Msg {
						text, isErr, err := proc.ExecuteCommand(cmdDef.Name, args)
						if err != nil {
							return tui.CommandResultMsg{Text: err.Error(), IsError: true}
						}
						return tui.CommandResultMsg{Text: text, IsError: isErr}
					}
				},
			})
		}
	}

	// Replay restored session messages into the chat view.
	if restoredSessionID != "" && len(restoredMsgs) > 0 {
		app.RestoreSession(restoredSessionID, restoredMsgs)
	}

	// Auto-submit initial prompt from CLI args (e.g. @filepath).
	if initialPrompt != "" {
		app.SetInitialPrompt(initialPrompt)
	}

	// Show setup message if no provider is configured
	if providerErr != nil {
		app.ShowWelcome(fmt.Sprintf(
			"No API key configured. To get started:\n\n"+
				"  /login anthropic          — OAuth login (Claude Pro/Max)\n"+
				"  /login openai             — OAuth login (ChatGPT Plus/Pro)\n"+
				"  export ANTHROPIC_API_KEY=sk-...  — API key\n"+
				"  export OPENAI_API_KEY=sk-...     — OpenAI key\n"+
				"  export GEMINI_API_KEY=...        — Gemini key\n\n"+
				"Or save to ~/.gi/auth.json.\n"+
				"Use /auth to check status. (%v)", providerErr))
	}

	p := tea.NewProgram(app, tea.WithAltScreen())

	// Create a map of plugin processes for UI response handling
	pluginsByName := make(map[string]*plugin.PluginProcess)
	for _, proc := range pluginMgr.Plugins() {
		pluginsByName[proc.Name()] = proc
	}

	// Wire up callbacks
	app.SetCallbacks(
		// onSubmit
		func(text string) {
			go func() {
				// Save user message to session
				msg := ai.NewTextMessage(ai.RoleUser, text)
				if err := sessionMgr.SaveMessage(msg); err != nil {
					log.Printf("Failed to save user message: %v", err)
				}

				// Run agent
				events := agentLoop.Events()
				go func() {
					if err := agentLoop.Prompt(ctx, text); err != nil {
						p.Send(tui.AgentErrorMsg{Err: err})
					}
				}()

				for event := range events {
					p.Send(tui.StreamEventMsg{Event: event})
					pluginMgr.ForwardEvent(event)

					// Save assistant and tool_result messages to session
					if (event.Type == agent.EventTurnEnd || event.Type == agent.EventToolResult) && event.Message != nil {
						if err := sessionMgr.SaveMessage(*event.Message); err != nil {
							log.Printf("Failed to save message: %v", err)
						}
					}
				}
			}()
		},
		// onSteer
		func(text string) {
			agentLoop.Steer(text)
		},
		// onCancel
		func() {
			agentLoop.Cancel()
		},
	)

	// Set UI response callback
	app.SetUIResponseCallback(func(response *tui.PluginUIResponseMsg) {
		if proc, ok := pluginsByName[response.PluginName]; ok {
			_ = proc.RespondToUIRequest(response.ID, response.Value, response.Closed, response.Error)
		}
	})

	// Consume inject messages from all plugins and route to TUI/agent.
	for _, proc := range pluginMgr.Plugins() {
		go func(proc *plugin.PluginProcess) {
			for msg := range proc.InjectMessages() {
				content := msg.Content
				if msg.Type == "log" {
					content = msg.Message
				}

				p.Send(tui.PluginInjectMsg{
					PluginName: proc.Name(),
					Content:    content,
					Role:       msg.Role,
					IsLog:      msg.Type == "log",
					LogLevel:   msg.Level,
				})

				// If the plugin injects a "user" role message, feed it to the agent.
				if msg.Type == "inject_message" && msg.Role == "user" && content != "" {
					agentLoop.FollowUp(content)
				}
			}
		}(proc)
	}

	// Consume UI requests from all plugins and route to TUI.
	for _, proc := range pluginMgr.Plugins() {
		go func(proc *plugin.PluginProcess) {
			for msg := range proc.UIRequests() {
				p.Send(tui.PluginUIRequestMsg{
					PluginName: proc.Name(),
					ID:         msg.ID,
					UIType:     msg.UIType,
					UITitle:    msg.UITitle,
					UIOptions:  msg.UIOptions,
					UIDefault:  msg.UIDefault,
					UILevel:    msg.UINotifyLevel,
				})
			}
		}(proc)
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// processFileArgs processes CLI positional arguments, expanding @filepath
// references into file contents. Arguments starting with @ are treated as file
// paths whose contents are included in the prompt. Other arguments are joined
// as plain text. Returns the combined initial prompt, or "" if no args given.
func processFileArgs(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}

	files := make([]string, 0, len(args))
	text := make([]string, 0, len(args))

	for _, arg := range args {
		if strings.HasPrefix(arg, "@") {
			path := arg[1:]
			if path == "" {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("cannot read %s: %w", path, err)
			}
			files = append(files, fmt.Sprintf("<file path=%q>\n%s\n</file>", path, strings.TrimRight(string(data), "\n")))
		} else {
			text = append(text, arg)
		}
	}

	var parts []string
	if len(files) > 0 {
		parts = append(parts, strings.Join(files, "\n\n"))
	}
	if len(text) > 0 {
		parts = append(parts, strings.Join(text, " "))
	}

	return strings.Join(parts, "\n\n"), nil
}
