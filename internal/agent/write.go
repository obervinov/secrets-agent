package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// WriteFileAtomic replaces path in a single rename, so a consumer reading the file
// concurrently never sees a partial secret. Both the file and its directory are
// synced: a hard reboot immediately after a write must not expose a zero-length file
// that would make compose interpolate every variable to empty.
func WriteFileAtomic(path string, content []byte, mode os.FileMode, group string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if group != "" {
		if err := chownGroup(tmpName, group); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func chownGroup(path, group string) error {
	lookup, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Errorf("group %s: %w", group, err)
	}
	gid, err := strconv.Atoi(lookup.Gid)
	if err != nil {
		return err
	}
	return os.Chown(path, 0, gid)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = handle.Close() }()
	return handle.Sync()
}

// Digest hashes the parts that decide whether a consumer needs re-applying.
func Digest(parts ...string) string {
	sum := sha256.New()
	for _, part := range parts {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}
