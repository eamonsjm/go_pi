package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/auth"
	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/mcp"
	"github.com/ejm/go_pi/pkg/plugin"
	"github.com/ejm/go_pi/pkg/rpc"
	"github.com/ejm/go_pi/pkg/session"
	"github.com/ejm/go_pi/pkg/skill"
	"github.com/ejm/go_pi/pkg/tools"
	"github.com/ejm/go_pi/pkg/tui"

	"golang.org/x/term"
)

func main() {
	os.Exit(run())
}

func run() int {
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
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Warning: could not determine home directory: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: could not determine working directory: %v", err)
	}

	// Set up approval for project-local plugins. Non-interactive modes
	// (print, json, rpc) deny project-local plugins by default — use
	// --plugin to load them explicitly.
	isInteractive := *printFlag == "" && !*jsonFlag && !*rpcFlag
	if isInteractive {
		pluginMgr.SetApprover(makePluginApprover(cfg))
	}

	var pluginDirs []plugin.DiscoverDir
	if home != "" {
		pluginDirs = append(pluginDirs, plugin.DiscoverDir{
			Path:   filepath.Join(home, ".gi", "plugins"),
			Source: plugin.SourceGlobal,
		})
	}
	if cwd != "" {
		pluginDirs = append(pluginDirs, plugin.DiscoverDir{
			Path:   filepath.Join(cwd, ".gi", "plugins"),
			Source: plugin.SourceProjectLocal,
		})
	}
	if err := pluginMgr.Discover(context.Background(), pluginDirs); err != nil {
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
	if err := pluginMgr.Initialize(context.Background(), plugin.Config{
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

	// Load skills from all three tiers: built-in (embed) < user < project.
	skillRegistry := skill.NewRegistry()
	if err := skill.LoadAll(context.Background(), skillRegistry, nil); err != nil {
		log.Printf("Failed to load skills: %v", err)
	}

	// Register the Skill tool so the LLM can invoke skills programmatically.
	skillTool := skill.NewSkillTool(skillRegistry, cfg.DefaultModel)
	registry.Register(skillTool)

	// Initialize MCP servers with sampling support.
	var mcpMgr *mcp.Manager
	var sb *samplingBridge
	if len(cfg.MCPServers) > 0 {
		sb = &samplingBridge{}
		mcpMgr = mcp.NewManager(mcp.ManagerConfig{
			ToolRegistry:    registry,
			SkillRegistry:   skillRegistry,
			WorkingDir:      cwd,
			ConfigDir:       cfg.ConfigDir,
			ProjectPath:     cwd,
			ClientName:      "gi",
			ClientVersion:   "0.1.0",
			SamplingHandler: sb.Handle,
			ConfirmSampling: sb.Confirm,
		})
		if err := mcpMgr.StartAll(context.Background(), cfg); err != nil {
			log.Printf("Failed to start MCP servers: %v", err)
		}
		defer mcpMgr.Shutdown(context.Background())
	}

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
		log.Printf("Failed to initialize auth: %v", authErr)
		return 1
	}

	// Resolve provider (may fail if no API key — that's ok for interactive mode)
	resolved, providerErr := resolveProvider(context.Background(), cfg, authResolver)
	provider := resolved.provider

	// Wire the provider into the sampling bridge so MCP servers can delegate
	// sampling requests to the configured AI provider.
	if sb != nil && provider != nil {
		sb.SetProvider(provider, cfg.DefaultModel)
	}

	// Print mode requires a working provider
	if *printFlag != "" {
		if providerErr != nil {
			log.Printf("Cannot use print mode: %v", providerErr)
			return 1
		}
		agentLoop, _ := makeAgentLoop(provider, registry, cfg, skillRegistry, mcpMgr)
		sessionMgr.NewSession()
		prompt := *printFlag
		if initialPrompt != "" {
			prompt = initialPrompt + "\n\n" + prompt
		}
		if code := runPrintMode(agentLoop, sessionMgr, prompt); code != 0 {
			return code
		}
		return 0
	}

	// JSON event stream mode
	if *jsonFlag {
		if providerErr != nil {
			log.Printf("Cannot use JSON mode: %v", providerErr)
			return 1
		}
		agentLoop, _ := makeAgentLoop(provider, registry, cfg, skillRegistry, mcpMgr)
		return rpc.RunJSONStream(agentLoop, initialPrompt)
	}

	// JSON-RPC 2.0 mode
	if *rpcFlag {
		if providerErr != nil {
			log.Printf("Cannot use RPC mode: %v", providerErr)
			return 1
		}
		agentLoop, _ := makeAgentLoop(provider, registry, cfg, skillRegistry, mcpMgr)
		rpc.RunRPC(agentLoop)
		return 0
	}

	// Create agent loop - may be nil provider if no API key configured
	var agentLoop *agent.Loop
	var mcpPermHook *mcp.PermissionHook
	if provider != nil {
		agentLoop, mcpPermHook = makeAgentLoop(provider, registry, cfg, skillRegistry, mcpMgr)
	} else {
		// Create a placeholder loop with no provider — submitting will show the error
		placeholderPrompt := buildSystemPrompt()
		if idx := skill.SkillSystemReminder(skillRegistry); idx != "" {
			placeholderPrompt += "\n\n<system-reminder>\n" + idx + "</system-reminder>"
		}
		agentLoop = agent.NewLoop(
			nil,
			registry,
			agent.WithModel(cfg.DefaultModel),
			agent.WithMaxTokens(cfg.MaxTokens),
			agent.WithThinking(ai.ThinkingLevel(cfg.ThinkingLevel)),
			agent.WithSystemPrompt(placeholderPrompt),
		)
	}

	// Restore or create session.
	var restoredMsgs []ai.Message
	var restoredSessionID string

	if *sessionFlag != "" {
		// Explicit --session flag: load the specified session.
		if err := sessionMgr.LoadSession(context.Background(), *sessionFlag); err != nil {
			log.Printf("Failed to load session: %v", err)
			return 1
		}
		restoredMsgs = sessionMgr.GetMessages()
		restoredSessionID = sessionMgr.CurrentID()
		agentLoop.SetMessages(restoredMsgs)
	} else if *newFlag {
		// Explicit --new flag: start fresh.
		sessionMgr.NewSession()
	} else {
		// Auto-resume the most recent session.
		latest, latestErr := sessionMgr.LatestSessionID(context.Background())
		if latestErr != nil {
			log.Printf("session: %v", latestErr)
		}
		if latest != "" {
			if err := sessionMgr.LoadSession(context.Background(), latest); err == nil {
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

	return runInteractive(agentLoop, sessionMgr, cfg, providerErr, pluginMgr, authStore, authResolver, skillRegistry, restoredSessionID, restoredMsgs, initialPrompt, mcpPermHook, sb)
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

type providerResult struct {
	provider     ai.Provider
	providerName string
	isOAuth      bool
}

func resolveProvider(ctx context.Context, cfg *config.Config, resolver *auth.Resolver) (providerResult, error) {
	providerName := cfg.DefaultProvider
	if providerName == "" {
		// Auto-detect based on available credentials.
		for _, name := range []string{"anthropic", "openrouter", "openai", "gemini", "azure"} {
			key, _ := resolver.Resolve(ctx, name)
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
			return providerResult{}, fmt.Errorf("no API key found. Set ANTHROPIC_API_KEY, OPENROUTER_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY, AZURE_OPENAI_API_KEY, AWS credentials, or OLLAMA_HOST, or use /login <provider>")
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
		p, err := ai.NewBedrockProvider(ctx, os.Getenv("AWS_REGION"))
		return providerResult{p, "bedrock", false}, err
	}

	// Ollama is local and needs no API key — just a host URL.
	if providerName == "ollama" {
		p, err := ai.NewOllamaProvider(os.Getenv("OLLAMA_HOST"))
		return providerResult{p, "ollama", false}, err
	}

	key, err := resolver.Resolve(ctx, providerName)
	if err != nil {
		return providerResult{}, fmt.Errorf("resolve %s credentials: %w", providerName, err)
	}
	if key == "" {
		return providerResult{}, fmt.Errorf("%s: no API key configured (set env var or use /login %s)", providerName, providerName)
	}

	switch providerName {
	case "anthropic":
		isOAuth := resolver.IsOAuthToken(providerName)
		if isOAuth {
			p, err := ai.NewAnthropicProviderWithToken(key)
			return providerResult{p, "anthropic", true}, err
		}
		p, err := ai.NewAnthropicProvider(key)
		return providerResult{p, "anthropic", false}, err
	case "openrouter":
		p, err := ai.NewOpenRouterProvider(key)
		return providerResult{p, "openrouter", false}, err
	case "openai":
		isOAuth := resolver.IsOAuthToken(providerName)
		p, err := ai.NewOpenAIProvider(key)
		return providerResult{p, "openai", isOAuth}, err
	case "gemini":
		p, err := ai.NewGeminiProvider(key)
		return providerResult{p, "gemini", false}, err
	case "azure":
		p, err := ai.NewAzureOpenAIProvider(key, "", "")
		return providerResult{p, "azure", false}, err
	default:
		return providerResult{}, fmt.Errorf("unknown provider: %s", providerName)
	}
}

func buildSystemPrompt() string {
	base := "You are Claude Code, Anthropic's official CLI for Claude."

	parts := make([]string, 0, 4)
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

func runPrintMode(agentLoop *agent.Loop, sessionMgr *session.Manager, prompt string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C and SIGTERM. The goroutine exits via ctx.Done when
	// the prompt ends, and signal.Stop unregisters the channel.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
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
			return 0
		case agent.EventAgentError:
			fmt.Fprintf(os.Stderr, "\nError: %v\n", event.Error)
			return 1
		}
	}
	return 0
}

func makeAgentLoop(provider ai.Provider, registry *tools.Registry, cfg *config.Config, skillReg *skill.Registry, mcpMgr *mcp.Manager) (*agent.Loop, *mcp.PermissionHook) {
	systemPrompt := buildSystemPrompt()
	if skillReg != nil {
		if idx := skill.SkillSystemReminder(skillReg); idx != "" {
			systemPrompt += "\n\n<system-reminder>\n" + idx + "</system-reminder>"
		}
	}
	if mcpMgr != nil {
		if instr := mcpMgr.ServerInstructions(); instr != "" {
			systemPrompt += "\n\n" + instr
		}
	}

	opts := []agent.Option{
		agent.WithModel(cfg.DefaultModel),
		agent.WithMaxTokens(cfg.MaxTokens),
		agent.WithThinking(ai.ThinkingLevel(cfg.ThinkingLevel)),
		agent.WithSystemPrompt(systemPrompt),
	}
	if mcpMgr != nil {
		opts = append(opts, agent.WithSystemMessageDrainer(mcpMgr.DrainSystemMessages))
	}

	loop := agent.NewLoop(provider, registry, opts...)

	// Register MCP permission hook AFTER RTK hooks (which are registered in
	// NewLoop) to preserve the original tool name for RTK translation.
	var permHook *mcp.PermissionHook
	if mcpMgr != nil {
		permConfigs := make(map[string]*config.MCPPermissionConfig)
		for name, srv := range cfg.MCPServers {
			if srv != nil && srv.Permissions != nil {
				permConfigs[name] = srv.Permissions
			}
		}
		permHook = mcp.NewPermissionHook(permConfigs, nil)
		permHook.SetAnnotationSource(mcp.NewAnnotationLookup(mcpMgr.GetAnnotations))
		loop.Hooks().Register(permHook)
	}

	return loop, permHook
}

func runInteractive(agentLoop *agent.Loop, sessionMgr *session.Manager, cfg *config.Config, providerErr error, pluginMgr *plugin.Manager, authStore *auth.Store, authResolver *auth.Resolver, skillReg *skill.Registry, restoredSessionID string, restoredMsgs []ai.Message, initialPrompt string, mcpPermHook *mcp.PermissionHook, sb *samplingBridge) int {
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
		result, err := resolveProvider(ctx, cfg, authResolver)
		if err != nil {
			log.Printf("Failed to resolve provider after login: %v", err)
			return
		}
		agentLoop.SetProvider(result.provider)
		agentLoop.SetModel(cfg.DefaultModel)
		app.SetModel(cfg.DefaultModel)
		if sb != nil {
			sb.SetProvider(result.provider, cfg.DefaultModel)
		}
	})

	// Register plugin-provided slash commands.
	for _, proc := range pluginMgr.Plugins() {
		for _, cmdDef := range proc.Commands() {
			app.RegisterCommand(&tui.SlashCommand{
				Name:        cmdDef.Name,
				Description: cmdDef.Description,
				Execute: func(args string) tea.Cmd {
					return func() tea.Msg {
						text, isErr, err := proc.ExecuteCommand(ctx, cmdDef.Name, args)
						if err != nil {
							return tui.CommandResultMsg{Text: err.Error(), IsError: true}
						}
						return tui.CommandResultMsg{Text: text, IsError: isErr}
					}
				},
			})
		}
	}

	// Register user-invocable skills as slash commands.
	if skillReg != nil {
		for _, s := range skillReg.UserInvocable() {
			s := s // capture for closure
			app.RegisterCommand(&tui.SlashCommand{
				Name:        s.Name,
				Description: s.Description,
				Execute: func(args string) tea.Cmd {
					return func() tea.Msg {
						body, err := s.LoadBody(ctx)
						if err != nil {
							return tui.CommandResultMsg{Text: fmt.Sprintf("Failed to load skill: %v", err), IsError: true}
						}
						argVars, err := skill.ParseSkillArgs(s.Arguments, args)
						if err != nil {
							return tui.CommandResultMsg{Text: err.Error(), IsError: true}
						}
						vars := skill.ContextVars(ctx, cfg.DefaultModel)
						for k, v := range argVars {
							vars[k] = v
						}
						rendered := skill.RenderTemplate(body, vars)
						display := "/" + s.Name
						if args != "" {
							display += " " + args
						}
						return tui.SkillInvokeMsg{Display: display, Prompt: rendered}
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
	app.SetProgram(p)
	if sb != nil {
		sb.SetProgram(p)
	}

	// Wire MCP permission hook to TUI confirmation now that the program exists.
	if mcpPermHook != nil {
		mcpPermHook.SetConfirm(app.ConfirmMCPTool)
	}

	// Create a map of plugin processes for UI response handling
	pluginsByName := make(map[string]*plugin.Process)
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
				if err := sessionMgr.SaveMessage(ctx, msg); err != nil {
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
						if err := sessionMgr.SaveMessage(ctx, *event.Message); err != nil {
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
			if err := proc.RespondToUIRequest(response.ID, response.Value, response.Closed, response.Error); err != nil {
				log.Printf("plugin %s: UI response error: %v", response.PluginName, err)
			}
		}
	})

	// pluginDone is closed when the TUI exits to cancel plugin consumer goroutines.
	pluginDone := make(chan struct{})

	// Consume inject messages from all plugins and route to TUI/agent.
	// Channels are re-acquired after plugin restart so consumers survive crashes.
	for _, proc := range pluginMgr.Plugins() {
		go func(proc *plugin.Process) {
			injectCh := proc.InjectMessages()
			for {
				select {
				case <-ctx.Done():
					return
				case <-pluginDone:
					return
				case msg, ok := <-injectCh:
					if !ok {
						// Channel closed — plugin may be restarting.
						if !waitForPluginRestart(ctx, proc, pluginDone) {
							return
						}
						newCh := proc.InjectMessages()
						if newCh == injectCh {
							return // Plugin did not restart — permanently dead.
						}
						injectCh = newCh
						continue
					}
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
			}
		}(proc)
	}

	// Consume UI requests from all plugins and route to TUI.
	// Channels are re-acquired after plugin restart so consumers survive crashes.
	for _, proc := range pluginMgr.Plugins() {
		go func(proc *plugin.Process) {
			uiCh := proc.UIRequests()
			for {
				select {
				case <-ctx.Done():
					return
				case <-pluginDone:
					return
				case msg, ok := <-uiCh:
					if !ok {
						// Channel closed — plugin may be restarting.
						if !waitForPluginRestart(ctx, proc, pluginDone) {
							return
						}
						newCh := proc.UIRequests()
						if newCh == uiCh {
							return // Plugin did not restart — permanently dead.
						}
						uiCh = newCh
						continue
					}
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
			}
		}(proc)
	}

	if _, err := p.Run(); err != nil {
		close(pluginDone)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	close(pluginDone)
	return 0
}

// waitForPluginRestart waits for a plugin to finish restarting after its
// communication channels have been closed. Returns true if the caller should
// re-acquire channels (a restart may have occurred), false if the consumer
// should exit (context cancelled, TUI exiting, or plugin permanently dead).
func waitForPluginRestart(ctx context.Context, proc *plugin.Process, done <-chan struct{}) bool {
	// Brief grace period for the supervisor to detect the process exit
	// and set the restarting flag.
	select {
	case <-ctx.Done():
		return false
	case <-done:
		return false
	case <-time.After(250 * time.Millisecond):
	}
	// Poll until the restart completes or the plugin is confirmed dead.
	for proc.Restarting() {
		select {
		case <-ctx.Done():
			return false
		case <-done:
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return true
}

// samplingBridge holds mutable state for MCP sampling callbacks. The AI
// provider and tea.Program may be set after the Manager is created.
type samplingBridge struct {
	mu       sync.Mutex
	provider ai.Provider
	model    string
	program  *tea.Program
}

// Handle implements mcp.SamplingHandler by delegating to the AI provider.
func (b *samplingBridge) Handle(ctx context.Context, serverName string, req mcp.SamplingRequest) (*mcp.SamplingResponse, error) {
	b.mu.Lock()
	p, model := b.provider, b.model
	b.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("no AI provider available for sampling")
	}
	return executeSampling(ctx, p, model, req)
}

// Confirm implements mcp.ConfirmSamplingFunc by sending a TUI confirmation
// prompt and blocking until the user responds.
func (b *samplingBridge) Confirm(serverName string, req mcp.SamplingRequest) (bool, error) {
	b.mu.Lock()
	prog := b.program
	b.mu.Unlock()
	if prog == nil {
		return false, fmt.Errorf("no interactive session available")
	}
	ch := make(chan bool, 1)
	prog.Send(tui.SamplingConfirmMsg{ServerName: serverName, ResponseCh: ch})
	return <-ch, nil
}

// SetProvider updates the AI provider and model used for sampling.
func (b *samplingBridge) SetProvider(p ai.Provider, model string) {
	b.mu.Lock()
	b.provider = p
	b.model = model
	b.mu.Unlock()
}

// SetProgram sets the tea.Program reference for interactive confirmation.
func (b *samplingBridge) SetProgram(p *tea.Program) {
	b.mu.Lock()
	b.program = p
	b.mu.Unlock()
}

// executeSampling converts an MCP sampling request into an AI provider call,
// streams the response, and returns the collected result.
func executeSampling(ctx context.Context, provider ai.Provider, model string, req mcp.SamplingRequest) (*mcp.SamplingResponse, error) {
	messages := make([]ai.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := ai.RoleUser
		if m.Role == "assistant" {
			role = ai.RoleAssistant
		}
		messages = append(messages, ai.NewTextMessage(role, m.Content.Text))
	}

	streamReq := ai.StreamRequest{
		Model:         model,
		SystemPrompt:  req.SystemPrompt,
		Messages:      messages,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		StopSequences: req.StopSequences,
	}

	events, err := provider.Stream(ctx, streamReq)
	if err != nil {
		return nil, fmt.Errorf("sampling stream: %w", err)
	}

	var text strings.Builder
	for event := range events {
		switch event.Type {
		case ai.EventTextDelta:
			text.WriteString(event.Delta)
		case ai.EventError:
			if event.Error != nil {
				return nil, fmt.Errorf("sampling stream error: %w", event.Error)
			}
		}
	}

	return &mcp.SamplingResponse{
		Role:    "assistant",
		Content: mcp.ContentItem{Type: "text", Text: text.String()},
		Model:   model,
	}, nil
}

// makePluginApprover returns a PluginApprover that checks the config for
// previously trusted paths and, if not found, prompts the user on stderr.
// If stdin is not a terminal (e.g. piped input), untrusted plugins are denied.
func makePluginApprover(cfg *config.Config) plugin.PluginApprover {
	return func(name, pluginDir string) (bool, error) {
		absDir, err := filepath.Abs(pluginDir)
		if err != nil {
			absDir = pluginDir
		}

		// Check if already trusted.
		for _, trusted := range cfg.TrustedProjectPlugins {
			if trusted == absDir {
				return true, nil
			}
		}

		// Non-terminal stdin means we can't prompt — deny by default.
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return false, nil
		}

		fmt.Fprintf(os.Stderr, "\nProject-local plugin detected: %s\n", name)
		fmt.Fprintf(os.Stderr, "  Path: %s\n", absDir)
		fmt.Fprintf(os.Stderr, "  Plugins from project directories can execute arbitrary code.\n")
		fmt.Fprintf(os.Stderr, "Allow this plugin? [y/N/always] ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return false, nil
		}
		response = strings.TrimSpace(strings.ToLower(response))

		switch response {
		case "y", "yes":
			return true, nil
		case "a", "always":
			cfg.TrustedProjectPlugins = append(cfg.TrustedProjectPlugins, absDir)
			if err := cfg.Save(); err != nil {
				log.Printf("Warning: failed to save trusted plugin to config: %v", err)
			}
			return true, nil
		default:
			return false, nil
		}
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

	parts := make([]string, 0, 2)
	if len(files) > 0 {
		parts = append(parts, strings.Join(files, "\n\n"))
	}
	if len(text) > 0 {
		parts = append(parts, strings.Join(text, " "))
	}

	return strings.Join(parts, "\n\n"), nil
}
