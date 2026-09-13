package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCompose(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestComposeDigestFollowsFileContents(t *testing.T) {
	// The case this exists for: an image tag bumped in the compose file while every
	// variable stayed the same. Digesting only the path reported "compose unchanged"
	// and left the old containers running indefinitely.
	values := Values{"TOKEN": "unchanged"}

	path := writeCompose(t, "services:\n  caddy:\n    image: caddy:2.10.0\n")
	before, err := composeDigest(values, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := os.WriteFile(path, []byte("services:\n  caddy:\n    image: caddy:2.11.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := composeDigest(values, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if before == after {
		t.Fatal("digest did not change when the compose file changed")
	}
}

func TestComposeDigestFollowsValues(t *testing.T) {
	path := writeCompose(t, "services: {}\n")

	before, err := composeDigest(Values{"TOKEN": "old"}, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after, err := composeDigest(Values{"TOKEN": "new"}, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if before == after {
		t.Fatal("digest did not change when a value changed")
	}
}

func TestComposeDigestStableWhenNothingChanged(t *testing.T) {
	path := writeCompose(t, "services: {}\n")
	values := Values{"A": "1", "B": "2"}

	first, err := composeDigest(values, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := composeDigest(values, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first != second {
		t.Fatal("digest is not stable across calls, so every run would re-apply")
	}
}

func TestComposeDigestReportsAMissingFile(t *testing.T) {
	// Better to fail loudly than to hash an empty string and call it unchanged.
	if _, err := composeDigest(Values{}, filepath.Join(t.TempDir(), "absent.yml")); err == nil {
		t.Fatal("expected an error for a missing compose file")
	}
}
