package agent

import "testing"

func parse(t *testing.T, values map[string]string) (*Config, error) {
	t.Helper()
	return parseConfig(values, "/etc/secrets-agent.conf")
}

func minimal() map[string]string {
	return map[string]string{
		"AGENT_URL":    "https://secrets.example.com/v1/env/web-1",
		"AUTH_HEADERS": `{"CF-Access-Client-Id":"id","CF-Access-Client-Secret":"secret"}`,
		"COMPOSE_FILE": "/opt/configurations/docker-compose.yml",
	}
}

func TestParseConfigRejects(t *testing.T) {
	cases := map[string]func(map[string]string){
		"plaintext endpoint": func(v map[string]string) { v["AGENT_URL"] = "http://secrets.example.com/x" },
		"no endpoint":        func(v map[string]string) { delete(v, "AGENT_URL") },
		"no credentials":     func(v map[string]string) { delete(v, "AUTH_HEADERS") },
		"no consumer":        func(v map[string]string) { delete(v, "COMPOSE_FILE") },
		"bad headers":        func(v map[string]string) { v["AUTH_HEADERS"] = "not json" },
		"bad units":          func(v map[string]string) { v["SYSTEMD_UNITS"] = "not json" },
		"unit without prefix": func(v map[string]string) {
			v["SYSTEMD_UNITS"] = `[{"unit":"alloy.service"}]`
		},
		"bad file mode": func(v map[string]string) { v["FILES_MODE"] = "not octal" },
	}
	for name, mutate := range cases {
		values := minimal()
		mutate(values)
		if _, err := parse(t, values); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestParseConfigUnits(t *testing.T) {
	values := minimal()
	values["STATE_DIR"] = "/opt/secrets"
	values["SYSTEMD_UNITS"] = `[{"unit":"alloy.service","prefix":"ALLOY_","group":"alloy"},
	                            {"unit":"node-exporter.service","prefix":"NE_","env_file":"/etc/ne.env"}]`

	cfg, err := parse(t, values)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Units) != 2 {
		t.Fatalf("got %d units, want 2", len(cfg.Units))
	}
	// EnvFile is derived from the unit name when the config does not give one.
	if got := cfg.Units[0].EnvFile; got != "/opt/secrets/alloy.env" {
		t.Errorf("derived env file = %q", got)
	}
	if got := cfg.Units[1].EnvFile; got != "/etc/ne.env" {
		t.Errorf("explicit env file = %q", got)
	}
}

func TestParseConfigUnitsOnlyIsEnough(t *testing.T) {
	// A host that feeds only systemd units needs no compose file.
	values := minimal()
	delete(values, "COMPOSE_FILE")
	values["SYSTEMD_UNITS"] = `[{"unit":"alloy.service","prefix":"ALLOY_"}]`

	if _, err := parse(t, values); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
