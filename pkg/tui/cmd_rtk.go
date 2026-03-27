package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/tools"
)

// rtkDisplayMsg carries formatted RTK status and metrics to show in chat.
type rtkDisplayMsg struct {
	text string
}

// rtkUpdatedMsg signals that an RTK setting was changed.
type rtkUpdatedMsg struct {
	setting string
	value   string
}

// isValidCategory reports whether s is a recognised RTK compression category.
func isValidCategory(s string) bool {
	switch s {
	case "git", "docker", "build", "package", "test", "file", "other":
		return true
	default:
		return false
	}
}

// NewRTKCommand returns a SlashCommand for /rtk that displays RTK metrics and
// configuration. Usage: /rtk [subcommand] [args]
func NewRTKCommand(cfg *config.Config, metrics *tools.Metrics) *SlashCommand {
	return &SlashCommand{
		Name:        "rtk",
		Description: "Show RTK metrics and configuration. Usage: /rtk [status|metrics|config|enable|disable|level|toggle|export]",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)

			// Default to status if no args
			if args == "" {
				return rtkStatus(cfg, metrics)
			}

			parts := strings.SplitN(args, " ", 2)
			subcommand := strings.TrimSpace(parts[0])

			switch subcommand {
			case "status":
				return rtkStatus(cfg, metrics)
			case "metrics":
				return rtkMetrics(metrics)
			case "config":
				return rtkShowConfig(cfg)
			case "enable":
				if len(parts) < 2 {
					return rtkError("missing category. Usage: /rtk enable <category>")
				}
				category := strings.TrimSpace(parts[1])
				return rtkEnableCategory(category, cfg)
			case "disable":
				if len(parts) < 2 {
					return rtkError("missing category. Usage: /rtk disable <category>")
				}
				category := strings.TrimSpace(parts[1])
				return rtkDisableCategory(category, cfg)
			case "level":
				if len(parts) < 2 {
					return rtkError("missing args. Usage: /rtk level <compressor> <low|medium|high>")
				}
				args := strings.TrimSpace(parts[1])
				levelParts := strings.SplitN(args, " ", 2)
				if len(levelParts) < 2 {
					return rtkError("invalid args. Usage: /rtk level <compressor> <low|medium|high>")
				}
				compressor := levelParts[0]
				level := levelParts[1]
				return rtkSetCompressionLevel(compressor, level, cfg)
			case "toggle":
				return rtkToggle(cfg)
			case "export":
				if cfg.RTK == nil {
					return rtkError("RTK not configured")
				}
				path := cfg.RTK.ExportPath
				if len(parts) > 1 {
					path = strings.TrimSpace(parts[1])
				}
				return rtkExport(path, metrics)
			default:
				return rtkError(fmt.Sprintf("unknown subcommand %q. Valid: status, metrics, config, enable, disable, level, toggle, export", subcommand))
			}
		},
	}
}

// rtkStatus shows a summary of RTK state, metrics, and compression stats.
func rtkStatus(cfg *config.Config, metrics *tools.Metrics) tea.Cmd {
	return func() tea.Msg {
		rtk := cfg.RTK
		if rtk == nil {
			return rtkDisplayMsg{text: "RTK is not configured"}
		}

		status := "disabled"
		if rtk.Enabled {
			status = "enabled"
		}

		metricsStatus := "disabled"
		if rtk.MetricsEnabled {
			metricsStatus = "enabled"
		}

		totalTokens := metrics.GetTotalTokens()
		savedTokens := metrics.GetSavedTokens()
		compressionRatio := 0.0
		if totalTokens > 0 {
			compressionRatio = float64(savedTokens*100) / float64(totalTokens)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "RTK Status:\n")
		fmt.Fprintf(&sb, "  Status:              %s\n", status)
		fmt.Fprintf(&sb, "  Metrics:             %s\n", metricsStatus)
		fmt.Fprintf(&sb, "  Total Tokens:        %d\n", totalTokens)
		fmt.Fprintf(&sb, "  Saved Tokens:        %d (%.1f%% reduction)\n", savedTokens, compressionRatio)

		sb.WriteString("\nActive Compressors:\n")
		for _, name := range []string{"go-test", "go-build", "git-log", "linter", "generic"} {
			fmt.Fprintf(&sb, "  %-13s %s\n", name+":", rtk.CompressionLevels[name])
		}

		sb.WriteString("\nEnabled Categories:\n")
		for _, name := range []string{"git", "docker", "build", "package", "test", "file", "other"} {
			fmt.Fprintf(&sb, "  %-10s %v\n", name+":", rtk.EnabledCategories[name])
		}

		sb.WriteString("\nCommands:\n")
		sb.WriteString("  /rtk status              - Show this status\n")
		sb.WriteString("  /rtk metrics             - Show detailed metrics\n")
		sb.WriteString("  /rtk config              - Show full configuration\n")
		sb.WriteString("  /rtk enable <category>   - Enable a category\n")
		sb.WriteString("  /rtk disable <category>  - Disable a category\n")
		sb.WriteString("  /rtk level <comp> <lvl>  - Set compression level\n")
		sb.WriteString("  /rtk toggle              - Toggle RTK on/off")

		return rtkDisplayMsg{text: sb.String()}
	}
}

// rtkMetrics shows detailed per-category metrics.
func rtkMetrics(metrics *tools.Metrics) tea.Cmd {
	return func() tea.Msg {
		text := "RTK Metrics by Category:\n"
		commands := metrics.GetCommandMetrics()

		if len(commands) == 0 {
			text += "\nNo metrics recorded yet. Run some commands to collect data."
		} else {
			for _, category := range []tools.CommandCategory{
				tools.CategoryGit,
				tools.CategoryDocker,
				tools.CategoryBuild,
				tools.CategoryPackage,
				tools.CategoryTest,
				tools.CategoryFile,
				tools.CategoryOther,
			} {
				if cm, ok := commands[category]; ok {
					if cm.Count > 0 {
						ratio := 0.0
						if cm.TotalBytes > 0 {
							ratio = float64((cm.TotalBytes-cm.CompressedBytes)*100) / float64(cm.TotalBytes)
						}
						text += fmt.Sprintf(
							"\n%s:\n"+
								"  Commands:        %d\n"+
								"  Total Bytes:     %d\n"+
								"  Compressed:      %d (%.1f%% reduction)\n"+
								"  Avg Time:        %v\n",
							category,
							cm.Count,
							cm.TotalBytes,
							cm.CompressedBytes,
							ratio,
							cm.AvgTime,
						)
					}
				}
			}
		}

		text += fmt.Sprintf("\n\nTotal tokens saved: %d", metrics.GetSavedTokens())

		return rtkDisplayMsg{text: text}
	}
}

// rtkShowConfig displays the full RTK configuration.
func rtkShowConfig(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		rtk := cfg.RTK
		if rtk == nil {
			return rtkDisplayMsg{text: "RTK is not configured"}
		}

		text := "RTK Configuration:\n\n"
		text += fmt.Sprintf("enabled: %v\n", rtk.Enabled)
		text += fmt.Sprintf("metrics_enabled: %v\n", rtk.MetricsEnabled)
		text += fmt.Sprintf("export_path: %s\n\n", rtk.ExportPath)

		text += "compression_levels:\n"
		for k, v := range rtk.CompressionLevels {
			text += fmt.Sprintf("  %s: %s\n", k, v)
		}

		text += "\nenabled_categories:\n"
		for k, v := range rtk.EnabledCategories {
			text += fmt.Sprintf("  %s: %v\n", k, v)
		}

		return rtkDisplayMsg{text: text}
	}
}

// rtkEnableCategory enables compression for a category.
func rtkEnableCategory(category string, cfg *config.Config) tea.Cmd {
	if cfg.RTK == nil {
		return rtkError("RTK not configured")
	}

	if !isValidCategory(category) {
		return rtkError(fmt.Sprintf("invalid category %q", category))
	}

	if cfg.RTK.EnabledCategories == nil {
		cfg.RTK.EnabledCategories = make(map[string]bool)
	}
	cfg.RTK.EnabledCategories[category] = true
	if err := cfg.Save(); err != nil {
		return rtkError(fmt.Sprintf("enabled %s but failed to save: %v", category, err))
	}

	return func() tea.Msg {
		return rtkUpdatedMsg{setting: "enable_" + category, value: "true"}
	}
}

// rtkDisableCategory disables compression for a category.
func rtkDisableCategory(category string, cfg *config.Config) tea.Cmd {
	if cfg.RTK == nil {
		return rtkError("RTK not configured")
	}

	if !isValidCategory(category) {
		return rtkError(fmt.Sprintf("invalid category %q", category))
	}

	if cfg.RTK.EnabledCategories == nil {
		cfg.RTK.EnabledCategories = make(map[string]bool)
	}
	cfg.RTK.EnabledCategories[category] = false
	if err := cfg.Save(); err != nil {
		return rtkError(fmt.Sprintf("disabled %s but failed to save: %v", category, err))
	}

	return func() tea.Msg {
		return rtkUpdatedMsg{setting: "disable_" + category, value: "true"}
	}
}

// rtkSetCompressionLevel sets the compression level for a compressor.
func rtkSetCompressionLevel(compressor, level string, cfg *config.Config) tea.Cmd {
	if cfg.RTK == nil {
		return rtkError("RTK not configured")
	}

	// Validate level
	validLevels := map[string]bool{"low": true, "medium": true, "high": true}
	if !validLevels[level] {
		return rtkError(fmt.Sprintf("invalid level %q — must be low, medium, or high", level))
	}

	// Validate compressor
	validCompressors := map[string]bool{
		"go-test": true, "go-build": true, "git-log": true, "linter": true, "generic": true,
	}
	if !validCompressors[compressor] {
		return rtkError(fmt.Sprintf("invalid compressor %q", compressor))
	}

	if cfg.RTK.CompressionLevels == nil {
		cfg.RTK.CompressionLevels = make(map[string]string)
	}
	cfg.RTK.CompressionLevels[compressor] = level
	if err := cfg.Save(); err != nil {
		return rtkError(fmt.Sprintf("set %s to %s but failed to save: %v", compressor, level, err))
	}

	return func() tea.Msg {
		return rtkUpdatedMsg{setting: compressor, value: level}
	}
}

// rtkToggle toggles RTK on/off.
func rtkToggle(cfg *config.Config) tea.Cmd {
	if cfg.RTK == nil {
		return rtkError("RTK not configured")
	}

	cfg.RTK.Enabled = !cfg.RTK.Enabled
	newStatus := "enabled"
	if !cfg.RTK.Enabled {
		newStatus = "disabled"
	}

	if err := cfg.Save(); err != nil {
		return rtkError(fmt.Sprintf("toggled RTK to %s but failed to save: %v", newStatus, err))
	}

	return func() tea.Msg {
		return rtkUpdatedMsg{setting: "enabled", value: newStatus}
	}
}

// rtkError returns a Cmd that sends a CommandResultMsg with an error.
func rtkError(msg string) tea.Cmd {
	return func() tea.Msg {
		return CommandResultMsg{Text: msg, IsError: true}
	}
}

// rtkExport exports metrics to a JSON file.
func rtkExport(path string, metrics *tools.Metrics) tea.Cmd {
	return func() tea.Msg {
		// Prepare export data
		export := map[string]interface{}{
			"total_tokens": metrics.GetTotalTokens(),
			"saved_tokens": metrics.GetSavedTokens(),
			"commands":     metrics.GetCommandMetrics(),
		}

		// Create parent directory if needed
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return CommandResultMsg{Text: fmt.Sprintf("failed to create directory: %v", err), IsError: true}
			}
		}

		// Marshal to JSON
		data, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			return CommandResultMsg{Text: fmt.Sprintf("failed to marshal metrics: %v", err), IsError: true}
		}

		// Write to file
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			return CommandResultMsg{Text: fmt.Sprintf("failed to write metrics to %s: %v", path, err), IsError: true}
		}

		return CommandResultMsg{Text: fmt.Sprintf("Metrics exported to %s", path), IsError: false}
	}
}
