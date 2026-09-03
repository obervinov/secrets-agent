package agent

import (
	"strings"
	"testing"
)

func TestNormalizeScalarTypes(t *testing.T) {
	values, err := Normalize(map[string]any{
		"TEXT":    "plain",
		"YES":     true,
		"NO":      false,
		"PORT":    float64(8080),
		"RATIO":   1.5,
		"MISSING": nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"TEXT":    "plain",
		"YES":     "true",
		"NO":      "false",
		"PORT":    "8080",
		"RATIO":   "1.5",
		"MISSING": "",
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Errorf("%s = %q, want %q", key, values[key], expected)
		}
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := map[string]map[string]any{
		"empty payload":  {},
		"nested object":  {"NESTED": map[string]any{"a": 1}},
		"array":          {"LIST": []any{"a"}},
		"key with space": {"BAD KEY": "x"},
		"key with dash":  {"BAD-KEY": "x"},
		"key with equal": {"BAD=KEY": "x"},
		"leading digit":  {"2FA": "x"},
		"empty key":      {"": "x"},
	}
	for name, payload := range cases {
		if _, err := Normalize(payload); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestDecodeDoesNotLeakPayload(t *testing.T) {
	// encoding/json embeds the offending input in its error message, which here would
	// be secret material.
	secret := "S3cr3tP@ssw0rd-do-not-leak"
	_, err := Decode([]byte(secret))
	if err == nil {
		t.Fatal("expected an error for a non-JSON payload")
	}
	if strings.Contains(err.Error(), "S3cr3t") {
		t.Fatalf("error message leaked the payload: %q", err)
	}
}

func TestRenderSystemdEnvEscapes(t *testing.T) {
	rendered, err := Values{
		"PLAIN": "value",
		"QUOTE": `has"quote`,
		"SLASH": `has\slash`,
		"SPACE": "has space",
		"HASH":  "has#hash",
	}.RenderSystemdEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		`HASH="has#hash"`,
		`PLAIN="value"`,
		`QUOTE="has\"quote"`,
		`SLASH="has\\slash"`,
		`SPACE="has space"`,
	}
	got := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), rendered)
	}
	for index, line := range want {
		if got[index] != line {
			t.Errorf("line %d = %q, want %q", index, got[index], line)
		}
	}
}

func TestRenderSystemdEnvRejectsNewline(t *testing.T) {
	// A newline cannot be represented in EnvironmentFile format at all, so it must
	// fail rather than be written as garbage that only surfaces as an auth failure.
	if _, err := (Values{"KEY": "line1\nline2"}).RenderSystemdEnv(); err == nil {
		t.Fatal("expected an error for a value containing a newline")
	}
}

func TestEnvironNeedsNoEscaping(t *testing.T) {
	// exec takes values verbatim, which is why compose gets them this way instead of
	// through a rendered dotenv file.
	pairs := Values{"AWKWARD": `a'b"c$d#e f`}.Environ()
	if len(pairs) != 1 || pairs[0] != `AWKWARD=a'b"c$d#e f` {
		t.Fatalf("got %q", pairs)
	}
}

func TestMergeOverlayWins(t *testing.T) {
	merged := Merge(Values{"A": "tf", "B": "tf"}, Values{"B": "store"})
	if merged["A"] != "tf" || merged["B"] != "store" {
		t.Fatalf("got %v", merged)
	}
}

func TestSubset(t *testing.T) {
	subset := Values{"ALLOY_ONE": "1", "OTHER": "2", "ALLOY_TWO": "3"}.Subset("ALLOY_")
	if len(subset) != 2 || subset["ALLOY_ONE"] != "1" || subset["ALLOY_TWO"] != "3" {
		t.Fatalf("got %v", subset)
	}
}
