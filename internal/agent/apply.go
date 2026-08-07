package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// alloyDropIn adds a second EnvironmentFile to the unit. The package ships
// /etc/default/alloy as its own conffile containing CONFIG_FILE and CUSTOM_ARGS, so
// rendering over that file would leave alloy with no config path at all.
const alloyDropIn = "/etc/systemd/system/alloy.service.d/10-secrets-agent.conf"

// validFileName guards the routed-file names, which become path components and come
// from a file users are told to edit.
var validFileName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Applier tracks each consumer independently. A consumer's applied-state is recorded
// only after its command succeeds, so a failed restart is retried on the next run
// instead of being reported as "no changes" forever.
type Applier struct {
	Config   *Config
	Log      Logger
	applied  map[string]string
	failures []string
}

func NewApplier(cfg *Config, log Logger) (*Applier, error) {
	applied, err := ReadKeyValueFile(cfg.AppliedPath())
	if err != nil {
		return nil, err
	}
	return &Applier{Config: cfg, Log: log, applied: applied}, nil
}

func (a *Applier) Failures() []string { return a.failures }

func (a *Applier) fail(format string, args ...any) {
	a.failures = append(a.failures, fmt.Sprintf(format, args...))
}

// Save persists the applied-state. It is written even when a consumer failed, so the
// consumers that did succeed are not re-applied on the next run.
func (a *Applier) Save() error {
	keys := make([]string, 0, len(a.applied))
	for key := range a.applied {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&out, "%s=%s\n", key, a.applied[key])
	}
	return WriteFileAtomic(a.Config.AppliedPath(), []byte(out.String()), 0o600, "")
}

// Compose hands the variables to docker compose in its environment. Nothing is
// rendered as dotenv text, so no quoting or escaping can go wrong on this path.
func (a *Applier) Compose(values Values) {
	digest := Digest(strings.Join(values.Environ(), "\n"), a.Config.ComposeFile)
	if a.applied["compose"] == digest {
		a.Log.Infof("compose unchanged")
		return
	}

	a.Log.Infof("applying compose (%d variables)", len(values))
	command := exec.Command("docker", "compose", "-f", a.Config.ComposeFile,
		"up", "-d", "--remove-orphans")
	command.Env = append(os.Environ(), values.Environ()...)
	command.Stdout = a.Log.Out
	command.Stderr = a.Log.Err

	if err := command.Run(); err != nil {
		a.fail("compose: %v", err)
		return
	}
	a.applied["compose"] = digest
}

// Alloy renders the ALLOY_* subset for the unit to read. Hosts without alloy are
// skipped rather than failed, otherwise the whole run aborts on a host that simply
// does not run it and no other consumer is ever applied.
func (a *Applier) Alloy(values Values) {
	subset := values.Subset("ALLOY_")
	if len(subset) == 0 {
		return
	}
	if !unitExists("alloy.service") {
		a.Log.Infof("alloy.service not present, skipping its %d variables", len(subset))
		return
	}

	content, err := subset.RenderSystemdEnv()
	if err != nil {
		a.fail("alloy: %v", err)
		return
	}

	dropInChanged, err := ensureAlloyDropIn(a.Config.AlloyEnv)
	if err != nil {
		a.fail("alloy drop-in: %v", err)
		return
	}

	digest := Digest(content, a.Config.AlloyEnv)
	if a.applied["alloy"] == digest && !dropInChanged {
		a.Log.Infof("alloy unchanged")
		return
	}

	a.Log.Infof("applying alloy (%d variables)", len(subset))
	if err := WriteFileAtomic(a.Config.AlloyEnv, []byte(content), 0o640, "alloy"); err != nil {
		a.fail("alloy env file: %v", err)
		return
	}
	if err := run("systemctl", "restart", "alloy.service"); err != nil {
		a.fail("alloy restart: %v", err)
		return
	}
	a.applied["alloy"] = digest
}

// Files writes one file per routed variable, for images that read *_FILE instead of an
// environment variable, and prunes files that are no longer routed.
func (a *Applier) Files(values Values) {
	routing, err := ReadKeyValueFile(a.Config.RoutingFile)
	if err != nil {
		a.fail("routing file: %v", err)
		return
	}

	wanted := make(map[string]string, len(routing))
	for name, variable := range routing {
		if !validFileName.MatchString(name) {
			a.Log.Warnf("routing entry %q is not a valid file name, skipped", name)
			continue
		}
		if _, ok := values[variable]; !ok {
			a.Log.Warnf("%s routes missing variable %s", name, variable)
			continue
		}
		wanted[name] = variable
	}

	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+values[wanted[name]])
	}
	digest := Digest(parts...)

	if err := os.MkdirAll(a.Config.FilesDir(), 0o700); err != nil {
		a.fail("files dir: %v", err)
		return
	}

	for _, name := range names {
		path := filepath.Join(a.Config.FilesDir(), name)
		// No trailing newline: several entrypoints feed the file content straight into
		// a password field.
		if err := WriteFileAtomic(path, []byte(values[wanted[name]]), a.Config.FilesMode, ""); err != nil {
			a.fail("file %s: %v", name, err)
			return
		}
	}

	entries, err := os.ReadDir(a.Config.FilesDir())
	if err != nil {
		a.fail("files dir: %v", err)
		return
	}
	for _, entry := range entries {
		if _, ok := wanted[entry.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(a.Config.FilesDir(), entry.Name())); err != nil {
			a.Log.Warnf("could not prune %s: %v", entry.Name(), err)
			continue
		}
		a.Log.Infof("pruned no longer routed file %s", entry.Name())
	}

	if a.applied["files"] != digest {
		a.Log.Infof("applied %d routed file(s)", len(names))
		a.applied["files"] = digest
	}
}

func ensureAlloyDropIn(envPath string) (bool, error) {
	wanted := fmt.Sprintf("[Service]\nEnvironmentFile=%s\n", envPath)

	existing, err := os.ReadFile(alloyDropIn)
	if err == nil && string(existing) == wanted {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	if err := os.MkdirAll(filepath.Dir(alloyDropIn), 0o755); err != nil {
		return false, err
	}
	if err := WriteFileAtomic(alloyDropIn, []byte(wanted), 0o644, ""); err != nil {
		return false, err
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return false, err
	}
	return true, nil
}

func unitExists(unit string) bool {
	return exec.Command("systemctl", "cat", unit).Run() == nil
}

func run(name string, args ...string) error {
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, firstLine(output))
	}
	return nil
}
