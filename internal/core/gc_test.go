package core

import (
	"testing"
	"time"
)

func TestGCRemovals(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	envs := map[string]*Env{
		"live":     {VMName: "trr-live"},                                // present, no TTL: keep
		"future":   {VMName: "trr-future", Expires: now.Add(time.Hour)}, // TTL not yet up: keep
		"expired":  {VMName: "trr-expired", Expires: now.Add(-time.Minute)},
		"dangling": {VMName: "trr-dangling"}, // VM gone: collect
	}
	exists := map[string]bool{
		"trr-live":    true,
		"trr-future":  true,
		"trr-expired": true,
		// trr-dangling deliberately absent
	}

	got := gcRemovals(envs, exists, now)
	if len(got) != 2 {
		t.Fatalf("expected 2 removals, got %d: %+v", len(got), got)
	}
	// Sorted by name: dangling before expired.
	if got[0].Name != "dangling" || got[1].Name != "expired" {
		t.Errorf("unexpected removals or order: %+v", got)
	}
	for _, r := range got {
		if r.Reason == "" {
			t.Errorf("%s has no reason", r.Name)
		}
	}
}

// A TTL exactly at now is up: an env is collectable the instant it expires,
// not a tick later.
func TestGCExpiryBoundary(t *testing.T) {
	now := time.Now()
	envs := map[string]*Env{"e": {VMName: "trr-e", Expires: now}}
	exists := map[string]bool{"trr-e": true}
	if got := gcRemovals(envs, exists, now); len(got) != 1 {
		t.Errorf("an env whose TTL is exactly now should be collectable, got %+v", got)
	}
}

// No TTL means no age-based collection, however old the env.
func TestGCLeavesUntaggedEnvs(t *testing.T) {
	now := time.Now()
	envs := map[string]*Env{"old": {VMName: "trr-old", Created: now.Add(-1000 * time.Hour)}}
	exists := map[string]bool{"trr-old": true}
	if got := gcRemovals(envs, exists, now); len(got) != 0 {
		t.Errorf("an env with no TTL must not be collected, got %+v", got)
	}
}
