package core

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func portEngine(t *testing.T, statePath string) *Engine {
	t.Helper()
	st, err := readStateFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	st.path = statePath
	st.loadedGoldens = cloneGoldens(st.Goldens)
	st.loadedEnvs = cloneEnvs(st.Envs)
	return &Engine{St: st}
}

// Each engine stands in for a separate terrarium process: its own copy of the
// state, loaded before any of the others chose a port.
func TestFreePortConcurrentCallersGetDistinctPorts(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	const n = 12
	engines := make([]*Engine, n)
	for i := range engines {
		engines[i] = portEngine(t, statePath)
	}

	ports := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range engines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ports[i], errs[i] = engines[i].freePort()
		}()
	}
	wg.Wait()

	seen := map[int]bool{}
	for i, p := range ports {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if seen[p] {
			t.Fatalf("port %d handed out twice: %v", p, ports)
		}
		seen[p] = true
	}
}

// A process that loaded its state earlier must still see the port of an env
// another process recorded since, even with that env shut down and its claim
// long expired.
func TestFreePortSeesEnvsSavedByOthers(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	stale := portEngine(t, statePath)

	other := portEngine(t, statePath)
	taken, err := other.freePort()
	if err != nil {
		t.Fatal(err)
	}
	other.St.Envs["api"] = &Env{VMName: "trr-api", SSHPort: taken}
	if err := other.St.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(statePath), portClaimDir, strconv.Itoa(taken))); err != nil {
		t.Fatal(err)
	}

	got, err := stale.freePort()
	if err != nil {
		t.Fatal(err)
	}
	if got == taken {
		t.Fatalf("port %d belongs to an env on disk and was handed out again", taken)
	}
}

func TestFreePortReusesExpiredClaim(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	e := portEngine(t, statePath)

	first, err := e.freePort()
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.freePort()
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("port %d handed out twice while its claim was fresh", first)
	}

	claim := filepath.Join(filepath.Dir(statePath), portClaimDir, strconv.Itoa(first))
	old := time.Now().Add(-2 * portClaimTTL)
	if err := os.Chtimes(claim, old, old); err != nil {
		t.Fatal(err)
	}
	third, err := e.freePort()
	if err != nil {
		t.Fatal(err)
	}
	if third != first {
		t.Errorf("expired claim on %d should be reusable, got %d", first, third)
	}
}
