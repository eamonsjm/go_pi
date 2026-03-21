package tui

import (
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
)

func TestHeader_Defaults(t *testing.T) {
	h := NewHeader()
	if h.model != "claude-sonnet-4-6" {
		t.Errorf("expected default model, got %q", h.model)
	}
	if h.thinkingLevel != ai.ThinkingOff {
		t.Errorf("expected ThinkingOff, got %q", h.thinkingLevel)
	}
}

func TestHeader_SetModel(t *testing.T) {
	h := NewHeader()
	h.SetModel("gpt-4o")
	if h.model != "gpt-4o" {
		t.Errorf("expected gpt-4o, got %q", h.model)
	}
}

func TestHeader_SetThinking(t *testing.T) {
	h := NewHeader()
	h.SetThinking(ai.ThinkingHigh)
	if h.thinkingLevel != ai.ThinkingHigh {
		t.Errorf("expected ThinkingHigh, got %q", h.thinkingLevel)
	}
}

func TestHeader_SetSession(t *testing.T) {
	h := NewHeader()
	h.SetSession("my-session")
	if h.sessionName != "my-session" {
		t.Errorf("expected my-session, got %q", h.sessionName)
	}
}

func TestHeader_Height(t *testing.T) {
	h := NewHeader()
	if h.Height() != 1 {
		t.Errorf("expected height 1, got %d", h.Height())
	}
}

func TestHeader_SetWidth(t *testing.T) {
	h := NewHeader()
	h.SetWidth(120)
	if h.width != 120 {
		t.Errorf("expected width 120, got %d", h.width)
	}
}

func TestHeader_View_ContainsModel(t *testing.T) {
	h := NewHeader()
	h.SetWidth(80)
	h.SetModel("test-model")
	view := h.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "test-model") {
		t.Errorf("expected View to contain model name, got %q", stripped)
	}
}

func TestHeader_View_ContainsThinking(t *testing.T) {
	h := NewHeader()
	h.SetWidth(80)
	h.SetThinking(ai.ThinkingHigh)
	view := h.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "thinking: high") {
		t.Errorf("expected View to contain thinking indicator, got %q", stripped)
	}
}

func TestHeader_View_NoThinkingWhenOff(t *testing.T) {
	h := NewHeader()
	h.SetWidth(80)
	h.SetThinking(ai.ThinkingOff)
	view := h.View()
	stripped := stripAnsi(view)
	if strings.Contains(stripped, "thinking:") {
		t.Errorf("expected no thinking indicator when off, got %q", stripped)
	}
}

func TestHeader_View_ContainsSession(t *testing.T) {
	h := NewHeader()
	h.SetWidth(80)
	h.SetSession("my-session")
	view := h.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "my-session") {
		t.Errorf("expected View to contain session name, got %q", stripped)
	}
}

func TestHeader_View_ContainsGi(t *testing.T) {
	h := NewHeader()
	h.SetWidth(80)
	view := h.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "gi") {
		t.Errorf("expected View to contain app name 'gi', got %q", stripped)
	}
}
