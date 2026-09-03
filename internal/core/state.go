package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

type Golden struct {
	VMName   string `json:"vm_name"`
	UUID     string `json:"uuid"`
	Snapshot string `json:"snapshot"`
	SSHUser  string `json:"ssh_user,omitempty"`
	SSHKey   string `json:"ssh_key,omitempty"`
	// stored in plain text: local dev tool, guest-only credentials
	SSHPassword string `json:"ssh_password,omitempty"`
	// Shell is what an SSH session lands in on this golden's guests - posix,
	// cmd or powershell - and so how exec has to quote a command for them.
	// Empty on records written before it existed, and on adopted Windows VMs
	// that have not been reached yet: those are probed on first exec.
	Shell string `json:"shell,omitempty"`
	// Owned marks a VM terrarium built itself, so it is ours to delete.
	// Adopted VMs belong to the user and are left alone.
	Owned bool `json:"owned,omitempty"`
}

// hasCreds reports whether terrarium can reach this golden's guests over SSH.
// An adopted VM may have none - an old Windows, a GUI-only appliance - and its
// forks are driven through the console instead.
func (g *Golden) hasCreds() bool {
	return g != nil && g.SSHUser != "" && (g.SSHKey != "" || g.SSHPassword != "")
}

type Env struct {
	VMName  string    `json:"vm_name"`
	UUID    string    `json:"uuid,omitempty"`
	Golden  string    `json:"golden"`
	SSHPort int       `json:"ssh_port"`
	Created time.Time `json:"created"`
	// Share is the host folder mounted at GuestSharePath, empty if none.
	Share string `json:"share,omitempty"`
	// RDPPort is the host port forwarded to the guest's own RDP server, set
	// once `terrarium rdp` has turned it on.
	RDPPort int `json:"rdp_port,omitempty"`
	// Expires is when `terrarium gc` may remove this env. Zero means never:
	// TTLs are opt-in, so an env forked without one lives until deleted.
	Expires time.Time `json:"expires,omitempty"`
}

// State is terrarium's record of what it manages. VMs not in here are never
// modified.
type State struct {
	Goldens map[string]*Golden `json:"goldens"`
	Envs    map[string]*Env    `json:"envs"`

	path string
	// loaded* are deep copies of what was on disk when this State was read.
	// Save diffs against them so it can reapply only what this process changed
	// onto the current file, rather than overwriting an env another process
	// added or removed in between.
	loadedGoldens map[string]*Golden
	loadedEnvs    map[string]*Env
}

const (
	lockFileName = "state.lock"
	lockTimeout  = 30 * time.Second
)

func cloneGoldens(m map[string]*Golden) map[string]*Golden {
	out := make(map[string]*Golden, len(m))
	for k, v := range m {
		c := *v
		out[k] = &c
	}
	return out
}

func cloneEnvs(m map[string]*Env) map[string]*Env {
	out := make(map[string]*Env, len(m))
	for k, v := range m {
		c := *v
		out[k] = &c
	}
	return out
}

// dataDir holds everything terrarium owns on the host: state, downloaded
// images, generated keys and seeds.
func dataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "terrarium"), nil
}

func statePath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

func LoadState() (*State, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	s, err := readStateFile(p)
	if err != nil {
		return nil, err
	}
	s.path = p
	s.loadedGoldens = cloneGoldens(s.Goldens)
	s.loadedEnvs = cloneEnvs(s.Envs)
	return s, nil
}

// readStateFile reads state.json into a bare State (maps only). A missing file
// is an empty state, not an error: the first command creates it.
func readStateFile(path string) (*State, error) {
	s := &State{Goldens: map[string]*Golden{}, Envs: map[string]*Env{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Goldens == nil {
		s.Goldens = map[string]*Golden{}
	}
	if s.Envs == nil {
		s.Envs = map[string]*Env{}
	}
	return s, nil
}

// mergeInto applies the changes this process made - measured against what it
// loaded - onto base, the current on-disk state. An entry we added or changed
// is written; one we removed is deleted; one we never touched is left as base
// has it, so a concurrent writer's edit to a different env survives. Two
// processes editing the same env is the one case this cannot reconcile: there,
// the last to save wins that env.
func (s *State) mergeInto(base *State) {
	for k, v := range s.Goldens {
		if old, ok := s.loadedGoldens[k]; !ok || !reflect.DeepEqual(old, v) {
			base.Goldens[k] = v
		}
	}
	for k := range s.loadedGoldens {
		if _, ok := s.Goldens[k]; !ok {
			delete(base.Goldens, k)
		}
	}
	for k, v := range s.Envs {
		if old, ok := s.loadedEnvs[k]; !ok || !reflect.DeepEqual(old, v) {
			base.Envs[k] = v
		}
	}
	for k := range s.loadedEnvs {
		if _, ok := s.Envs[k]; !ok {
			delete(base.Envs, k)
		}
	}
}

// Save rewrites the state file. It holds guest passwords in plain text, so it
// is 0600 like the keys and the .rdp file. An exclusive lock and a re-read of
// the current file make a concurrent CLI command and MCP server safe; the
// write itself goes to a temp file and is renamed, so a crash mid-write cannot
// truncate the only record of every VM terrarium owns.
func (s *State) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	unlock, err := lockState(filepath.Join(dir, lockFileName), lockTimeout)
	if err != nil {
		return err
	}
	defer unlock()

	base, err := readStateFile(s.path)
	if err != nil {
		return err
	}
	s.mergeInto(base)

	data, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	// The snapshots have to move to what was just written, or a later delete of
	// something this process itself created is invisible to mergeInto - not in
	// loaded*, so nothing to remove - and the next Save writes the record
	// straight back. That is the fork rollback path. An env another process
	// added is in neither map either way, so it still survives the merge.
	s.loadedGoldens = cloneGoldens(s.Goldens)
	s.loadedEnvs = cloneEnvs(s.Envs)
	return nil
}
