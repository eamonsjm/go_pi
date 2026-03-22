//go:build !linux

package plugin

import (
	"strings"
	"testing"
)

func TestSetMemoryLimit_ReturnsError(t *testing.T) {
	err := setMemoryLimit(1, 512)
	if err == nil {
		t.Fatal("setMemoryLimit should return an error on non-Linux platforms")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, want it to contain 'not supported'", err.Error())
	}
}
