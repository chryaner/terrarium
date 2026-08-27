package core

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/vbox"
)

// goldenNameRe allows dots (ubuntu-24.04) that env names do not.
var goldenNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)

// promoteChecks validates a promotion without touching VirtualBox, so the
// rules are testable without one. It returns the env to promote and the new
// golden record, credentials inherited from the env's own golden: the disk is
// a copy, so whoever could log in before can log in after.
func promoteChecks(st *State, envName, image string) (*Env, *Golden, error) {
	env := st.Envs[envName]
	if env == nil {
		return nil, nil, fmt.Errorf("no env %q", envName)
	}
	if !goldenNameRe.MatchString(image) {
		return nil, nil, fmt.Errorf("invalid golden name %q (letters, digits, dots, dashes)", image)
	}
	if st.Goldens[image] != nil {
		return nil, nil, fmt.Errorf("golden %q already exists: pick another name", image)
	}
	g := &Golden{VMName: goldenPrefix + image}
	if src := st.Goldens[env.Golden]; src != nil {
		g.SSHUser = src.SSHUser
		g.SSHPassword = src.SSHPassword
		g.SSHKey = src.SSHKey
	}
	return env, g, nil
}

// Promote flattens an env's current state into a new standalone golden, so a
// configured machine becomes a fork source like any other image. The copy is
// full, not linked: it costs the disk and minutes of a golden, and in return
// depends on nothing - the env, its golden and their snapshots can all go and
// the promoted image still forks. The env itself is shut down and left in
// place.
func (e *Engine) Promote(envName, image string, progress func(string)) (*Golden, error) {
	env, g, err := promoteChecks(e.St, envName, image)
	if err != nil {
		return nil, err
	}
	if _, err := e.findVM(g.VMName); err == nil {
		return nil, fmt.Errorf("%s already exists in VirtualBox", g.VMName)
	}
	progress("shutting " + envName + " down")
	if err := e.Down(envName); err != nil {
		return nil, err
	}
	progress(fmt.Sprintf("flattening %s into %s (full copy: minutes, not seconds)", env.VMName, g.VMName))
	if err := e.VB.CloneFull(env.VMName, g.VMName); err != nil {
		return nil, err
	}
	return e.recordGolden(image, g, progress)
}

// RemoveGolden deletes a golden's record, and its VM and disks when terrarium
// built them. Refused while forks depend on it: they are linked clones of its
// snapshot, and unregistering it would take their disks with it. An adopted
// VM belongs to the user, so only the record goes.
func (e *Engine) RemoveGolden(image string) error {
	g := e.St.Goldens[image]
	if g == nil {
		return fmt.Errorf("no golden %q", image)
	}
	if forks := e.forksOf(image); len(forks) > 0 {
		return fmt.Errorf("%s is still forked by %s: `terrarium rm` them first", image, strings.Join(forks, ", "))
	}
	if g.Owned {
		// Mirror Remove: a VM already deleted outside terrarium is fine, the
		// record still has to go.
		if err := e.VB.PowerOff(g.VMName); err == nil {
			if err := e.VB.WaitOff(g.VMName, 30*time.Second); err != nil {
				return err
			}
			if err := e.VB.Unregister(g.VMName); err != nil && !vbox.IsNotRegistered(err) {
				return err
			}
		} else if !vbox.IsNotRegistered(err) {
			return err
		}
	}
	delete(e.St.Goldens, image)
	return e.St.Save()
}
