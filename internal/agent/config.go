package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Config is read from a single file on the host. It holds the credential that
// unlocks every secret for that host, which is why LoadConfig refuses to use a
// file that anyone other than root can read.
type Config struct {
	URL         string
	AuthHeaders map[string]string
	ComposeFile string
	StateDir    string
	TFEnvFile   string
	RoutingFile string
	Units       []UnitConsumer
	FilesMode   os.FileMode
}

// UnitConsumer is a systemd unit that takes some of the variables through an
// EnvironmentFile. Configured rather than hardcoded: which units exist, and which
// variables belong to them, is a property of the host, not of this program.
type UnitConsumer struct {
	// Unit is the systemd unit to restart, e.g. "alloy.service".
	Unit string `json:"unit"`
	// Prefix selects the variables this unit gets, e.g. "ALLOY_".
	Prefix string `json:"prefix"`
	// EnvFile is where they are rendered. Defaults to <state_dir>/<unit>.env.
	EnvFile string `json:"env_file"`
	// Group, when set, owns the rendered file so a non-root unit can read it.
	Group string `json:"group"`
}

func (c *Config) CachePath() string   { return filepath.Join(c.StateDir, "cache.json") }
func (c *Config) AppliedPath() string { return filepath.Join(c.StateDir, "applied") }
func (c *Config) LockPath() string    { return filepath.Join(c.StateDir, "lock") }
func (c *Config) FilesDir() string    { return filepath.Join(c.StateDir, "files") }

// LoadConfig reads KEY=value pairs, rejecting a config whose permissions would
// leak the credential rather than documenting that it must be protected.
func LoadConfig(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("config %s: cannot determine ownership", path)
	}
	if stat.Uid != 0 {
		return nil, fmt.Errorf("config %s must be owned by root, is uid %d", path, stat.Uid)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("config %s must be mode 0600, is %04o", path, perm)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	return parseConfig(parseKeyValues(string(raw)), path)
}

// parseConfig is split out so the rules below are testable without being root, which
// LoadConfig deliberately requires.
func parseConfig(values map[string]string, path string) (*Config, error) {
	cfg := &Config{
		URL:         values["AGENT_URL"],
		ComposeFile: values["COMPOSE_FILE"],
		StateDir:    orDefault(values["STATE_DIR"], "/opt/secrets"),
		RoutingFile: orDefault(values["ROUTING_FILE"], "/etc/secrets-agent.files"),
		FilesMode:   0o644,
	}
	cfg.TFEnvFile = orDefault(values["TF_ENV"], filepath.Join(cfg.StateDir, "terraform.env"))

	if units := values["SYSTEMD_UNITS"]; units != "" {
		if err := json.Unmarshal([]byte(units), &cfg.Units); err != nil {
			return nil, fmt.Errorf("SYSTEMD_UNITS is not a JSON array: %w", err)
		}
		for index := range cfg.Units {
			unit := &cfg.Units[index]
			if unit.Unit == "" || unit.Prefix == "" {
				return nil, fmt.Errorf("SYSTEMD_UNITS[%d] needs both unit and prefix", index)
			}
			if unit.EnvFile == "" {
				unit.EnvFile = filepath.Join(cfg.StateDir,
					strings.TrimSuffix(unit.Unit, ".service")+".env")
			}
		}
	}

	if mode := values["FILES_MODE"]; mode != "" {
		parsed, err := strconv.ParseUint(mode, 8, 32)
		if err != nil {
			return nil, fmt.Errorf("FILES_MODE %q is not an octal mode", mode)
		}
		cfg.FilesMode = os.FileMode(parsed)
	}

	if headers := values["AUTH_HEADERS"]; headers != "" {
		if err := json.Unmarshal([]byte(headers), &cfg.AuthHeaders); err != nil {
			return nil, fmt.Errorf("AUTH_HEADERS is not a JSON object: %w", err)
		}
	}

	if cfg.URL == "" {
		return nil, fmt.Errorf("config %s is missing AGENT_URL", path)
	}
	// Sending the credential in cleartext must fail, not warn.
	if !strings.HasPrefix(cfg.URL, "https://") {
		return nil, fmt.Errorf("AGENT_URL must be https, got %q", cfg.URL)
	}
	if len(cfg.AuthHeaders) == 0 {
		return nil, fmt.Errorf("config %s is missing AUTH_HEADERS", path)
	}
	if cfg.ComposeFile == "" && len(cfg.Units) == 0 {
		return nil, fmt.Errorf("config %s configures no consumer: set COMPOSE_FILE, SYSTEMD_UNITS or both", path)
	}

	return cfg, nil
}

// parseKeyValues reads the KEY=value format used by the config, the routing file and
// the applied-state file. Values may be wrapped in quotes.
func parseKeyValues(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

// ReadKeyValueFile returns an empty map when the file is absent: both the routing
// file and the applied-state file are legitimately missing on a first run.
func ReadKeyValueFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseKeyValues(string(raw)), nil
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
