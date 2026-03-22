package plugin

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSetMemoryLimit_SetsRlimit(t *testing.T) {
	// Save original limit so we can restore it.
	var orig unix.Rlimit
	if err := unix.Prlimit(os.Getpid(), unix.RLIMIT_AS, nil, &orig); err != nil {
		t.Fatalf("failed to read original RLIMIT_AS: %v", err)
	}
	t.Cleanup(func() {
		// Restore original limit (best effort).
		_ = unix.Prlimit(os.Getpid(), unix.RLIMIT_AS, &orig, nil)
	})

	const limitMB int64 = 4096
	if err := setMemoryLimit(os.Getpid(), limitMB); err != nil {
		t.Fatalf("setMemoryLimit(%d, %d) returned error: %v", os.Getpid(), limitMB, err)
	}

	// Read back and verify the limit was applied.
	var got unix.Rlimit
	if err := unix.Prlimit(os.Getpid(), unix.RLIMIT_AS, nil, &got); err != nil {
		t.Fatalf("failed to read RLIMIT_AS after set: %v", err)
	}

	wantBytes := uint64(limitMB) * 1024 * 1024
	if got.Cur != wantBytes {
		t.Errorf("RLIMIT_AS cur = %d, want %d", got.Cur, wantBytes)
	}
	if got.Max != wantBytes {
		t.Errorf("RLIMIT_AS max = %d, want %d", got.Max, wantBytes)
	}
}

func TestSetMemoryLimit_InvalidPID(t *testing.T) {
	// PID -1 is invalid; Prlimit should return ESRCH or similar.
	err := setMemoryLimit(-1, 512)
	if err == nil {
		t.Fatal("setMemoryLimit(-1, 512) should return an error for invalid PID")
	}
}
