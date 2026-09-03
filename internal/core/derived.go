package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/recipe"
	"github.com/chryaner/terrarium/internal/sshx"
)

// maxDerivedDepth stops a from-cycle (a builds on b builds on a) from
// recursing forever. Real chains are two or three deep.
const maxDerivedDepth = 8

// setupTailBytes keeps enough failing output to act on without flooding the
// error with a whole apt transcript.
const setupTailBytes = 4 << 10

// getDerived builds a derived recipe: fork the base golden, run the setup
// commands in the fork over SSH, flatten the result into a new golden, remove
// the scratch fork. The recipe is the shareable artifact - a team member with
// the same YAML builds an equivalent golden from their own base, no disk
// image changes hands.
func (e *Engine) getDerived(r recipe.Recipe, image string, cpus, memMB int, force bool, depth int, progress func(string)) (*Golden, error) {
	if depth >= maxDerivedDepth {
		return nil, fmt.Errorf("recipe %q: from-chain deeper than %d, check the recipes for a cycle", image, maxDerivedDepth)
	}
	if err := e.clearExisting(image, goldenPrefix+image, force, progress); err != nil {
		return nil, err
	}

	base := e.St.Goldens[r.From]
	if base == nil {
		progress(fmt.Sprintf("no golden %q yet: building the base first", r.From))
		var err error
		base, err = e.get(r.From, 0, 0, false, depth+1, progress)
		if err != nil {
			return nil, fmt.Errorf("building base %q: %w", r.From, err)
		}
	}
	if !base.hasCreds() {
		return nil, fmt.Errorf("recipe %q: base %q has no SSH credentials, so setup commands have nowhere to run", image, r.From)
	}

	scratch := scratchName(image)
	if e.St.Envs[scratch] != nil {
		return nil, fmt.Errorf("env %q already exists (a failed build leaves it for inspection): `terrarium rm %s` and retry", scratch, scratch)
	}
	progress(fmt.Sprintf("forking %s to run setup in", r.From))
	// A failed fork rolls itself back, so there is no scratch env left to hint
	// about here. A failed setup below is a different matter: that env is kept.
	if _, err := e.Fork(r.From, scratch, ForkOpts{CPUs: cpus, MemMB: memMB}, progress); err != nil {
		return nil, err
	}
	if err := e.runSetup(r, scratch, progress); err != nil {
		return nil, fmt.Errorf("%w\n%s was left for inspection: `terrarium ssh %s`, then `terrarium rm %s`",
			err, scratch, scratch, scratch)
	}
	g, err := e.Promote(scratch, image, progress)
	if err != nil {
		return nil, err
	}
	progress("removing build env " + scratch)
	if err := e.Remove(scratch); err != nil {
		return g, err
	}
	return g, nil
}

// scratchName is the env a derived build runs its setup in. Dots are legal in
// image names but not in env names.
func scratchName(image string) string {
	return strings.ReplaceAll(image, ".", "-") + "-build"
}

// runSetup executes the recipe's commands in order, each with its own
// timeout. Output is buffered rather than streamed: on success it is noise,
// on failure its tail is the diagnosis.
func (e *Engine) runSetup(r recipe.Recipe, envName string, progress func(string)) error {
	timeout := time.Duration(r.SetupTimeoutMin) * time.Minute
	for i, cmd := range r.Setup {
		progress(fmt.Sprintf("setup %d/%d: %s", i+1, len(r.Setup), cmd))
		var out sshx.OutputBuffer
		// Through Run, so a base golden on the guestcontrol transport builds
		// derived images the same way an SSH one does.
		code, err := e.run(context.Background(), envName, timeout, cmd, nil, &out, &out)
		if err != nil {
			return fmt.Errorf("setup %d/%d (%s): %w", i+1, len(r.Setup), cmd, err)
		}
		if code != 0 {
			return fmt.Errorf("setup %d/%d exited %d: %s\n%s", i+1, len(r.Setup), code, cmd, setupTail(out.String()))
		}
	}
	return nil
}

func setupTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= setupTailBytes {
		return s
	}
	return "..." + s[len(s)-setupTailBytes:]
}
