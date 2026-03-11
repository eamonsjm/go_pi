package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/plugin"
	"github.com/ejm/go_pi/pkg/session"
	"github.com/ejm/go_pi/pkg/tools"
	"github.com/ejm/go_pi/pkg/tui"
)

func main() {
	// Flags
	modelFlag := flag.String("model", "", "Model to use (e.g. claude-sonnet-4-20250514)")
	providerFlag := flag.String("provider", "", "Provider (anthropic, openai)")
	thinkingFlag := flag.String("thinking", "", "Thinking level (off, low, medium, high)")
	printFlag := flag.String("p", "", "Print mode: send prompt and print response")
	sessionFlag := flag.String("session", "", "Resume a session by ID")
	cwdFlag := flag.String("cwd", "", "Working directory")
	pluginFlag := flag.String("plugin", "", "Comma-separated paths to plugin executables or directories")
	flag.Parse()

	// Load config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Apply flag overrides
	if *modelFlag != "" {
		cfg.DefaultModel = *modelFlag
	}
	if *providerFlag != "" {
		cfg.DefaultProvider = *providerFlag
	}
	if *thinkingFlag != "" {
		cfg.ThinkingLevel = *thinkingFlag
	}

	// Set working directory
	if *cwdFlag != "" {
		if err := os.Chdir(*cwdFlag); err != nil {
			log.Fatalf("Failed to change directory: %v", err)
		}
	}

	// Set up tools
	registry := tools.NewRegistry()
	tools.RegisterDefaults(registry)

	// Set up plugin manager
	pluginMgr := plugin.NewManager(registry)
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	pluginMgr.Discover([]string{
		filepath.Join(home, ".pi", "plugins"),
		filepath.Join(cwd, ".pi", "plugins"),
	})
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
	pluginMgr.Initialize(plugin.PluginConfig{
		Cwd:       cwd,
		Model:     cfg.DefaultModel,
		Provider:  cfg.DefaultProvider,
		PiVersion: "0.1.0",
	})
	defer pluginMgr.Shutdown()

	// Set up session manager
	sessionDir := cfg.SessionDir
	if sessionDir == "" {
		home, _ := os.UserHomeDir()
		sessionDir = filepath.Join(home, ".pi", "sessions")
	}
	sessionMgr := session.NewManager(sessionDir)

	// Resolve provider (may fail if no API key — that's ok for interactive mode)
	provider, providerErr := resolveProvider(cfg)

	// Print mode requires a working provider
	if *printFlag != "" {
		if providerErr != nil {
			log.Fatalf("Cannot use print mode: %v", providerErr)
		}
		agentLoop := makeAgentLoop(provider, registry, cfg)
		sessionMgr.NewSession()
		runPrintMode(agentLoop, sessionMgr, *printFlag)
		return
	}

	// Interactive mode — launch TUI even without a provider
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

	// Restore session if requested
	if *sessionFlag != "" {
		if err := sessionMgr.LoadSession(*sessionFlag); err != nil {
			log.Fatalf("Failed to load session: %v", err)
		}
		msgs := sessionMgr.GetMessages()
		agentLoop.SetMessages(msgs)
	} else {
		sessionMgr.NewSession()
	}

	// Interactive mode
	runInteractive(agentLoop, sessionMgr, cfg, providerErr, pluginMgr)
}

func resolveProvider(cfg *config.Config) (ai.Provider, error) {
	auth, err := config.LoadAuth()
	if err != nil {
		return nil, fmt.Errorf("loading auth: %w", err)
	}

	providerName := cfg.DefaultProvider
	if providerName == "" {
		// Auto-detect based on available keys
		if auth.GetKey("anthropic") != "" {
			providerName = "anthropic"
		} else if auth.GetKey("openrouter") != "" {
			providerName = "openrouter"
		} else if auth.GetKey("openai") != "" {
			providerName = "openai"
		} else {
			return nil, fmt.Errorf("no API key found. Set ANTHROPIC_API_KEY, OPENROUTER_API_KEY, or OPENAI_API_KEY")
		}
	}

	model := cfg.DefaultModel
	if model == "" {
		switch providerName {
		case "anthropic":
			model = "claude-sonnet-4-20250514"
		case "openrouter":
			model = "anthropic/claude-sonnet-4-20250514"
		case "openai":
			model = "gpt-4o"
		}
		cfg.DefaultModel = model
	}

	switch providerName {
	case "anthropic":
		key := auth.GetKey("anthropic")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		p, err := ai.NewAnthropicProvider(key)
		if err != nil {
			return nil, err
		}
		return p, nil
	case "openrouter":
		key := auth.GetKey("openrouter")
		if key == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not set")
		}
		p, err := ai.NewOpenRouterProvider(key)
		if err != nil {
			return nil, err
		}
		return p, nil
	case "openai":
		key := auth.GetKey("openai")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not set")
		}
		p, err := ai.NewOpenAIProvider(key)
		if err != nil {
			return nil, err
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
}

func buildSystemPrompt() string {
	cwd, _ := os.Getwd()

	var sb strings.Builder
	sb.WriteString("You are pi, an AI coding assistant running in the user's terminal.\n")
	sb.WriteString("You help with software engineering tasks: writing code, debugging, explaining, refactoring.\n\n")
	sb.WriteString("## Environment\n")
	sb.WriteString(fmt.Sprintf("- Working directory: %s\n", cwd))
	sb.WriteString(fmt.Sprintf("- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH))
	sb.WriteString("- You have access to tools: read, write, edit, bash, glob, grep\n\n")

	sb.WriteString("## Guidelines\n")
	sb.WriteString("- Use tools to explore and modify the codebase\n")
	sb.WriteString("- Read files before editing them\n")
	sb.WriteString("- Be concise in your responses\n")
	sb.WriteString("- Use bash for running commands, tests, builds\n")
	sb.WriteString("- Use edit for targeted text replacements (not write for small changes)\n")
	sb.WriteString("- Use glob/grep to find files and search content\n")
	sb.WriteString("- Create parent directories when writing new files\n")
	sb.WriteString("- Be careful with destructive operations\n\n")

	// Load AGENTS.md or CLAUDE.md if present
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".pi/SYSTEM.md"} {
		path := filepath.Join(cwd, name)
		data, err := os.ReadFile(path)
		if err == nil {
			sb.WriteString(fmt.Sprintf("## Project Instructions (from %s)\n", name))
			sb.WriteString(string(data))
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
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

func runInteractive(agentLoop *agent.AgentLoop, sessionMgr *session.Manager, cfg *config.Config, providerErr error, pluginMgr *plugin.Manager) {
	app := tui.NewApp()
	app.SetModel(cfg.DefaultModel)
	app.SetThinking(cfg.ThinkingLevel)
	app.RegisterBuiltinCommands(agentLoop, sessionMgr, cfg)
	app.SetModelChangeCallback(func(provider, model string) {
		agentLoop.SetModel(model)
	})

	// Register plugin-provided slash commands.
	for _, proc := range pluginMgr.Plugins() {
		proc := proc // capture for closure
		for _, cmdDef := range proc.Commands() {
			cmdDef := cmdDef // capture for closure
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

	// Show setup message if no provider is configured
	if providerErr != nil {
		app.ShowWelcome(fmt.Sprintf(
			"No API key configured. To get started:\n\n"+
				"  export ANTHROPIC_API_KEY=sk-...\n"+
				"  export OPENROUTER_API_KEY=sk-...\n"+
				"  export OPENAI_API_KEY=sk-...\n\n"+
				"Or save to ~/.pi/auth.json:\n"+
				"  {\"keys\": {\"anthropic\": \"sk-...\"}}\n\n"+
				"Then restart pi. (%v)", providerErr))
	}

	// Create the Bubble Tea program
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseAllMotion())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Wire up callbacks
	app.SetCallbacks(
		// onSubmit
		func(text string) {
			go func() {
				// Save user message to session
				msg := ai.NewTextMessage(ai.RoleUser, text)
				sessionMgr.SaveMessage(msg)

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

					// Save assistant messages to session
					if event.Type == agent.EventTurnEnd && event.Message != nil {
						sessionMgr.SaveMessage(*event.Message)
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

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
