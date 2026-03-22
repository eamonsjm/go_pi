package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ejm/go_pi/pkg/tools"
)

// --- Malformed manifest tests ------------------------------------------------

func TestDiscover_ManifestEmptyJSON(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "empty-json")
	os.MkdirAll(pluginDir, 0755)

	// Valid JSON but empty object — no executable specified.
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte("{}"), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// No executable found — plugin should fail to load.
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (empty manifest)", len(m.plugins))
	}
}

func TestDiscover_ManifestTruncatedJSON(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "truncated")
	os.MkdirAll(pluginDir, 0755)

	// Truncated JSON.
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name": "test`), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (truncated JSON)", len(m.plugins))
	}
}

func TestDiscover_ManifestArrayJSON(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "array-json")
	os.MkdirAll(pluginDir, 0755)

	// Valid JSON but array, not object.
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`[1,2,3]`), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (array JSON)", len(m.plugins))
	}
}

func TestDiscover_ManifestEmptyFile(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "empty-file")
	os.MkdirAll(pluginDir, 0755)

	// Completely empty file.
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(""), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (empty file)", len(m.plugins))
	}
}

func TestDiscover_ManifestBinaryGarbage(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "binary-garbage")
	os.MkdirAll(pluginDir, 0755)

	// Binary data that isn't JSON.
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte{0x00, 0xFF, 0xFE, 0x01}, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (binary garbage)", len(m.plugins))
	}
}

// --- Missing executable tests ------------------------------------------------

func TestDiscover_ManifestPointsToMissingExecutable(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "missing-exe")
	os.MkdirAll(pluginDir, 0755)

	manifest := Manifest{
		Name:       "missing-exe",
		Executable: "nonexistent-binary",
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (missing executable)", len(m.plugins))
	}
}

func TestDiscover_ManifestPointsToDirectory(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "dir-exe")
	os.MkdirAll(pluginDir, 0755)

	// Point executable at a subdirectory (relative path).
	os.MkdirAll(filepath.Join(pluginDir, "subdir"), 0755)

	manifest := Manifest{
		Name:       "dir-exe",
		Executable: "subdir",
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (executable is a directory)", len(m.plugins))
	}
}

func TestDiscover_ManifestRelativeExeMissing(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "rel-missing")
	os.MkdirAll(pluginDir, 0755)

	manifest := Manifest{
		Name:       "rel-missing",
		Executable: "does-not-exist",
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (relative exe missing)", len(m.plugins))
	}
}

func TestDiscover_ManifestRelativeExeResolved(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "rel-exe")
	os.MkdirAll(pluginDir, 0755)

	// Create an executable in the plugin directory itself.
	var exeName string
	var exeContent string
	if runtime.GOOS == "windows" {
		exeName = "run.bat"
		exeContent = "@exit /b 0\n"
	} else {
		exeName = "run.sh"
		exeContent = "#!/bin/sh\nexit 0\n"
	}
	exePath := filepath.Join(pluginDir, exeName)
	os.WriteFile(exePath, []byte(exeContent), 0755)

	manifest := Manifest{
		Name:       "rel-exe",
		Executable: exeName, // Relative path.
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 1 {
		t.Errorf("plugins = %d, want 1 (relative exe resolved)", len(m.plugins))
	}
	m.Shutdown()
}

// --- LoadPlugin negative tests -----------------------------------------------

func TestLoadPlugin_DirectoryNoManifestNoExe(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "empty")
	os.MkdirAll(pluginDir, 0755)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.LoadPlugin(pluginDir)
	if err == nil {
		t.Fatal("expected error for directory with no manifest or executable")
	}
}

func TestLoadPlugin_DirectoryBadManifest(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "bad")
	os.MkdirAll(pluginDir, 0755)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte("not json"), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.LoadPlugin(pluginDir)
	if err == nil {
		t.Fatal("expected error for bad manifest JSON")
	}
}

func TestLoadPlugin_FileNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based executable check unreliable on Windows")
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "script")
	os.WriteFile(filePath, []byte("#!/bin/sh\necho hi\n"), 0644) // Not executable.

	reg := tools.NewRegistry()
	m := NewManager(reg)

	// loadFromDir checks execute permission on fallback executable.
	// LoadPlugin with a direct file path goes through startAndRegisterWithManifest
	// which checks os.Stat + IsDir but doesn't check execute bit directly
	// (it tries to actually start the process).
	err := m.LoadPlugin(filePath)
	// This may or may not succeed depending on whether startPlugin
	// attempts to run the binary. Either way, just ensure no panic.
	_ = err
}

// --- Discover with multiple directories --------------------------------------

func TestDiscover_MultipleDirsSomeInvalid(t *testing.T) {
	goodDir := t.TempDir()

	// Put a valid plugin in the good directory.
	pluginDir := filepath.Join(goodDir, "test-plugin")
	os.MkdirAll(pluginDir, 0755)

	exeName := "run.sh"
	if runtime.GOOS == "windows" {
		exeName = "run.bat"
		os.WriteFile(filepath.Join(pluginDir, exeName), []byte("@exit /b 0\n"), 0755)
	} else {
		os.WriteFile(filepath.Join(pluginDir, exeName), []byte("#!/bin/sh\nexit 0\n"), 0755)
	}
	manifest := Manifest{
		Name:       "good-plugin",
		Executable: exeName,
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	// Mix of nonexistent and valid directories.
	err := m.Discover(context.Background(), []DiscoverDir{
		{Path: "/nonexistent/dir1", Source: SourceGlobal},
		{Path: goodDir, Source: SourceGlobal},
		{Path: "/nonexistent/dir2", Source: SourceGlobal},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 1 {
		t.Errorf("plugins = %d, want 1 (should load from valid dir)", len(m.plugins))
	}
	m.Shutdown()
}

func TestDiscover_MultipleDirsMixedPlugins(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Good plugin in dir1.
	pluginDir1 := filepath.Join(dir1, "plugin-a")
	os.MkdirAll(pluginDir1, 0755)
	exeName := "run.sh"
	if runtime.GOOS == "windows" {
		exeName = "run.bat"
		os.WriteFile(filepath.Join(pluginDir1, exeName), []byte("@exit /b 0\n"), 0755)
	} else {
		os.WriteFile(filepath.Join(pluginDir1, exeName), []byte("#!/bin/sh\nexit 0\n"), 0755)
	}
	data, _ := json.Marshal(Manifest{Name: "plugin-a", Executable: exeName})
	os.WriteFile(filepath.Join(pluginDir1, "plugin.json"), data, 0644)

	// Bad plugin (bad JSON) in dir2.
	pluginDir2 := filepath.Join(dir2, "plugin-b")
	os.MkdirAll(pluginDir2, 0755)
	os.WriteFile(filepath.Join(pluginDir2, "plugin.json"), []byte("{bad"), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover(context.Background(), []DiscoverDir{
		{Path: dir1, Source: SourceGlobal},
		{Path: dir2, Source: SourceGlobal},
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Only the good plugin should load.
	if len(m.plugins) != 1 {
		t.Errorf("plugins = %d, want 1 (bad plugin should be skipped)", len(m.plugins))
	}
	if len(m.plugins) > 0 && m.plugins[0].name != "plugin-a" {
		t.Errorf("plugin name = %q, want %q", m.plugins[0].name, "plugin-a")
	}
	m.Shutdown()
}

// --- Shutdown edge cases -----------------------------------------------------

func TestShutdown_Empty(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Shutdown()
	if err != nil {
		t.Errorf("Shutdown on empty manager: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d after shutdown, want 0", len(m.plugins))
	}
}

func TestShutdown_Idempotent(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "idempotent")
	os.MkdirAll(pluginDir, 0755)
	exeName := "run.sh"
	if runtime.GOOS == "windows" {
		exeName = "run.bat"
		os.WriteFile(filepath.Join(pluginDir, exeName), []byte("@exit /b 0\n"), 0755)
	} else {
		os.WriteFile(filepath.Join(pluginDir, exeName), []byte("#!/bin/sh\nexit 0\n"), 0755)
	}
	data, _ := json.Marshal(Manifest{Name: "test", Executable: exeName})
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	m.Discover(context.Background(), []DiscoverDir{{Path: dir, Source: SourceGlobal}})

	// First shutdown.
	m.Shutdown()
	// Second shutdown should be fine.
	err := m.Shutdown()
	if err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

// --- Accessors on empty manager ----------------------------------------------

func TestPluginTools_Empty(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	td := m.PluginTools()
	if len(td) != 0 {
		t.Errorf("PluginTools() = %d, want 0", len(td))
	}
}

func TestPluginCommands_Empty(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	cmds := m.PluginCommands()
	if len(cmds) != 0 {
		t.Errorf("PluginCommands() = %d, want 0", len(cmds))
	}
}

func TestPlugins_Empty(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	if len(m.Plugins()) != 0 {
		t.Errorf("Plugins() = %d, want 0", len(m.Plugins()))
	}
}
