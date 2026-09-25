package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each lockState call opens its own handle, so a second holder in the same
// process contends exactly like a second terrarium process would.
func TestLockStateExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)

	unlock, err := lockState(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if _, err := lockState(path, 200*time.Millisecond); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second holder should be refused as busy, got %v", err)
	}
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Errorf("refused after %v, before the timeout", waited)
	}

	unlock()
	again, err := lockState(path, time.Second)
	if err != nil {
		t.Fatalf("lock should be free after unlock: %v", err)
	}
	again()
}

func TestLockStateWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	unlock, err := lockState(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		unlock()
	}()
	next, err := lockState(path, 5*time.Second)
	if err != nil {
		t.Fatalf("a waiter should get the lock once it is released: %v", err)
	}
	next()
}
